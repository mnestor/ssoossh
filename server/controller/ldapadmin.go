package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// Probe binding sources. Typed values are what let an admin test an entry
// before that person has ever logged in.
const (
	probeBindingSelf   = "self"
	probeBindingUser   = "user"
	probeBindingCustom = "custom"
)

// NewLDAPAdminController registers the directory diagnostics on group.
//
// The split follows the existing one: the status read is auditor-scoped
// because it names no credential and answers "is the sync even running",
// while the sync and the probe are admin-only. The sync is not SOC despite
// being able to disable people, because it restores and refreshes access as
// readily as it removes it; the probe is not auditor because it makes the
// server open an outbound connection and read an entry in full.
func NewLDAPAdminController(
	group *gin.RouterGroup,
	c *config.Config,
	db *gorm.DB,
	ldap service.LDAPDiagnostics,
	sessionAuthMiddleware gin.HandlerFunc,
	adminAuthMiddleware gin.HandlerFunc,
	auditorAuthMiddleware gin.HandlerFunc,
	csrfMiddleware gin.HandlerFunc,
	probeRateLimit gin.HandlerFunc,
	auditService *service.AuditService,
) {
	a := &ldapAdminController{config: c, db: db, ldap: ldap, audit: auditService}

	auditorGroup := group.Group("/admin/ldap", sessionAuthMiddleware, auditorAuthMiddleware)
	auditorGroup.GET("/status", a.statusHandler)

	adminGroup := group.Group("/admin/ldap", sessionAuthMiddleware, adminAuthMiddleware, csrfMiddleware)
	adminGroup.POST("/sync", a.syncHandler)
	if probeRateLimit != nil {
		adminGroup.POST("/probe", probeRateLimit, a.probeHandler)
		return
	}
	adminGroup.POST("/probe", a.probeHandler)
}

// ldapAdminController handles the directory diagnostics.
type ldapAdminController struct {
	config *config.Config
	db     *gorm.DB
	// ldap is nil when directory enrichment is disabled, which every
	// handler reports as "nothing to do here" rather than 500ing.
	ldap  service.LDAPDiagnostics
	audit *service.AuditService
}

// statusHandler handles GET /api/admin/ldap/status.
//
// @Summary     Directory sync status and probe target (auditor-only)
// @Description Reports whether directory enrichment is enabled, the
// @Description connection and query the probe is pinned to, the sync
// @Description settings, and the most recent sync pass on any instance.
// @Description Names no credential: the bind password is never part of this
// @Description response. Answers "is the sync even running", which before
// @Description the run record could only be answered by reading a log on
// @Description whichever instance happened to run it.
// @Tags        admin
// @Produce     json
// @Success     200 {object} webtypes.LDAPStatusResponse "Directory status"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as auditor"
// @Security    sessionCookie
// @Router      /api/admin/ldap/status [get]
func (a *ldapAdminController) statusHandler(g *gin.Context) {
	if a.ldap == nil {
		respondData(g, webtypes.LDAPStatusResponse{Enabled: false})
		return
	}

	run, err := a.ldap.LastSyncRun(g.Request.Context())
	if err != nil {
		handleError(g, err)
		return
	}

	ldapCfg := a.config.LDAP
	resp := webtypes.LDAPStatusResponse{
		Enabled:               true,
		URL:                   ldapCfg.URL,
		BaseDN:                ldapCfg.BaseDN,
		UserFilter:            ldapCfg.UserFilter,
		ConfiguredAttributes:  configuredAttributes(ldapCfg),
		SyncIntervalSeconds:   int(ldapCfg.Sync.Interval.Seconds()),
		DisableAfterSeconds:   int(ldapCfg.Sync.DisableAfter.Seconds()),
		Reenable:              ldapCfg.Sync.Reenable,
		TLSInsecureSkipVerify: ldapCfg.TLSInsecureSkipVerify,
		Running:               a.ldap.SyncRunning(),
		LastRun:               a.newSyncRunResponse(g, run),
	}
	respondData(g, resp)
}

// syncHandler handles POST /api/admin/ldap/sync.
//
// @Summary     Run the directory sync now (admin-only)
// @Description Runs the same pass the scheduler runs, on this instance, and
// @Description returns what it concluded. One at a time: a pass already in
// @Description progress makes this a 409 rather than stacking a second one
// @Description over the same users. With dry_run the pass reads the
// @Description directory and changes nothing, reporting the disables and
// @Description re-enables it would have performed — which is what makes it
// @Description safe to press during an incident.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Param       request body webtypes.LDAPSyncRequestBody false "Sync options"
// @Success     200 {object} webtypes.LDAPSyncRunResponse "What the pass concluded"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid request body"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     409 {object} openapidoc.ErrorEnvelope "A sync is already running"
// @Failure     503 {object} openapidoc.ErrorEnvelope "The directory could not be reached"
// @Security    sessionCookie
// @Router      /api/admin/ldap/sync [post]
func (a *ldapAdminController) syncHandler(g *gin.Context) {
	if a.ldap == nil {
		handleError(g, &errorresponses.InvalidRequestError{
			Reason: "directory enrichment is disabled (ldap.enabled), so there is no sync to run",
		})
		return
	}

	var req webtypes.LDAPSyncRequestBody
	if err := g.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}

	actor := a.auditActor(g)
	opts := service.SyncOptions{
		Trigger: model.LDAPSyncTriggerManual,
		DryRun:  req.DryRun,
	}
	if actor != nil {
		opts.ActorUserID = actor.UserID
	}

	run, err := a.ldap.SyncWithOptions(g.Request.Context(), opts)
	if errors.Is(err, service.ErrSyncInProgress) {
		handleError(g, &errorresponses.ConflictError{
			Reason: "a directory sync is already running on this instance; wait for it to finish",
		})
		return
	}

	// A pass that could not reach the directory still produced a run row,
	// and that row is the answer: it says the pass ran and why it failed,
	// which is exactly what the operator pressed the button to find out.
	// So the error is recorded and reported in the body rather than
	// swallowing the record behind a bare 5xx.
	a.recordSyncAudit(g, actor, run, err)

	if run == nil {
		handleError(g, err)
		return
	}
	respondData(g, a.newSyncRunResponse(g, run))
}

// recordSyncAudit records the triggered sync with what it concluded.
func (a *ldapAdminController) recordSyncAudit(g *gin.Context, actor *service.AuditSubject, run *model.LDAPSyncRun, cause error) {
	detail := map[string]any{"dry_run": run != nil && run.DryRun}
	if run != nil {
		detail["run_id"] = run.ID
		detail["users"] = run.UsersSeen
		detail["found"] = run.Found
		detail["missing"] = run.Missing
		detail["failed"] = run.Failed
		detail["disabled"] = run.Disabled
		detail["reenabled"] = run.Reenabled
	}
	if cause != nil {
		detail["error"] = cause.Error()
	}

	a.auditRecord(g, service.AuditEvent{
		Action:     service.AuditLDAPSyncTriggered,
		Actor:      actor,
		OccurredAt: time.Now(),
		Detail:     detail,
	})
}

// probeHandler handles POST /api/admin/ldap/probe.
//
// @Summary     Probe the directory (admin-only, read-only)
// @Description Runs one directory lookup through the login path's three
// @Description stages — lookup, field resolution, then the merge and
// @Description allowlist — and stops before the write. Reports the filter it
// @Description actually sent, every attribute the directory returned rather
// @Description than the ones the configuration names, what each field
// @Description resolved to, which group values the allowlist would drop, and
// @Description the config lines that would keep what is currently ignored.
// @Description
// @Description The connection is not part of the request: the probe always
// @Description uses the running ldap.url, bind credentials and base_dn, so
// @Description it cannot be pointed at another host. It writes nothing.
// @Tags        admin
// @Accept      json
// @Produce     json
// @Param       request body webtypes.LDAPProbeRequestBody true "What to ask the directory"
// @Success     200 {object} webtypes.LDAPProbeResponse "What the probe found"
// @Failure     400 {object} openapidoc.ErrorEnvelope "Invalid request, filter or bindings"
// @Failure     401 {object} openapidoc.ErrorEnvelope "Not authenticated"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Not authorized as admin"
// @Failure     404 {object} openapidoc.ErrorEnvelope "The user to bind against does not exist"
// @Failure     429 {object} openapidoc.ErrorEnvelope "Too many probes"
// @Security    sessionCookie
// @Router      /api/admin/ldap/probe [post]
func (a *ldapAdminController) probeHandler(g *gin.Context) {
	if a.ldap == nil {
		handleError(g, &errorresponses.InvalidRequestError{
			Reason: "directory enrichment is disabled (ldap.enabled), so there is nothing to probe",
		})
		return
	}

	var req webtypes.LDAPProbeRequestBody
	if err := g.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		handleError(g, &errorresponses.InvalidRequestError{Reason: "invalid request body"})
		return
	}

	mode, err := probeMode(req.Mode)
	if err != nil {
		handleError(g, &errorresponses.InvalidRequestError{Reason: err.Error()})
		return
	}

	bindings, err := a.probeBindings(g, req)
	if err != nil {
		handleError(g, err)
		return
	}

	result, err := a.ldap.Probe(g.Request.Context(), service.ProbeRequest{
		Bindings:   bindings,
		Mode:       mode,
		Filter:     req.Filter,
		Attributes: req.Attributes,
	})
	if err != nil {
		// Everything that reaches here is a question the directory could
		// not be asked — an unparseable filter, an unreachable server —
		// rather than an answer about an entry, so it reads as a bad
		// request rather than a server fault.
		handleError(g, &errorresponses.InvalidRequestError{Reason: err.Error()})
		return
	}

	a.auditRecord(g, service.AuditEvent{
		Action:     service.AuditLDAPProbed,
		Actor:      a.auditActor(g),
		OccurredAt: time.Now(),
		Detail: map[string]any{
			"mode":        string(result.Mode),
			"base_dn":     result.BaseDN,
			"filter_sent": result.FilterSent,
			"matched":     result.Matched,
		},
	})

	respondData(g, newProbeResponse(result))
}

// probeMode validates the requested filter mode, defaulting to template
// because that is the mode that answers "what does my config send".
func probeMode(mode string) (service.ProbeFilterMode, error) {
	switch mode {
	case "", string(service.ProbeFilterTemplate):
		return service.ProbeFilterTemplate, nil
	case string(service.ProbeFilterLiteral):
		return service.ProbeFilterLiteral, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q", service.ProbeFilterTemplate, service.ProbeFilterLiteral)
	}
}

// probeBindings resolves what a template-mode filter renders against: the
// caller's own session, an existing user, or typed values.
func (a *ldapAdminController) probeBindings(g *gin.Context, req webtypes.LDAPProbeRequestBody) (service.ProbeBindings, error) {
	switch req.BindingSource {
	case probeBindingCustom:
		if req.Bindings == nil {
			return service.ProbeBindings{}, &errorresponses.InvalidRequestError{
				Reason: "binding_source \"custom\" needs a bindings object",
			}
		}
		return service.ProbeBindings{
			Username: req.Bindings.Username,
			Email:    req.Bindings.Email,
			Subject:  req.Bindings.Subject,
			Extra:    req.Bindings.Extra,
		}, nil

	case probeBindingUser:
		if req.UserID == "" {
			return service.ProbeBindings{}, &errorresponses.InvalidRequestError{
				Reason: "binding_source \"user\" needs a user_id",
			}
		}
		var user model.User
		if err := a.db.WithContext(g.Request.Context()).First(&user, "id = ?", req.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return service.ProbeBindings{}, &errorresponses.NotFoundError{
					Resource: fmt.Sprintf("user %q", req.UserID),
				}
			}
			return service.ProbeBindings{}, fmt.Errorf("failed to look up the user to bind against: %w", err)
		}
		return service.ProbeBindings{
			Username: user.Username,
			Email:    user.Email,
			Subject:  user.Subject,
			Extra:    storedExtraScalars(user.ExtraFields),
		}, nil

	case "", probeBindingSelf:
		identity, ok := middleware.Identity(g)
		if !ok || identity == nil {
			// not covered: the route sits behind SessionAuthMiddleware,
			// which aborts before the handler when there is no identity.
			return service.ProbeBindings{}, &errorresponses.ForbiddenError{}
		}
		return service.ProbeBindings{
			Username: identity.Username,
			Email:    identity.Email,
			Subject:  identity.Subject,
			Extra:    identity.ExtraScalars(),
		}, nil

	default:
		return service.ProbeBindings{}, &errorresponses.InvalidRequestError{
			Reason: fmt.Sprintf("binding_source must be %q, %q or %q", probeBindingSelf, probeBindingUser, probeBindingCustom),
		}
	}
}

// storedExtraScalars decodes a users row's extra fields into the scalar map
// a filter template renders against. Lists are skipped for the same reason
// the login path skips them: a filter interpolates one value.
func storedExtraScalars(encoded string) map[string]string {
	if encoded == "" {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
		return nil
	}
	out := map[string]string{}
	for name, value := range raw {
		if s, ok := value.(string); ok {
			out[name] = s
		}
	}
	return out
}

// configuredAttributes is every attribute name the configured fields read,
// sorted, so the console can highlight them in a returned entry.
func configuredAttributes(cfg config.LDAPConfig) []string {
	var names []string
	for _, field := range cfg.Fields {
		if field.Attribute != "" && !slices.Contains(names, field.Attribute) {
			names = append(names, field.Attribute)
		}
	}
	slices.Sort(names)
	return names
}

// newSyncRunResponse converts a run row to its wire shape, resolving the
// actor's username for display. A missing actor row leaves the name empty
// rather than failing the read: the run still happened.
func (a *ldapAdminController) newSyncRunResponse(g *gin.Context, run *model.LDAPSyncRun) *webtypes.LDAPSyncRunResponse {
	if run == nil {
		return nil
	}

	resp := &webtypes.LDAPSyncRunResponse{
		ID:         run.ID,
		StartedAt:  run.StartedAt,
		FinishedAt: run.FinishedAt,
		Trigger:    string(run.Trigger),
		DryRun:     run.DryRun,
		Instance:   run.Instance,
		UsersSeen:  run.UsersSeen,
		Found:      run.Found,
		Missing:    run.Missing,
		Failed:     run.Failed,
		Disabled:   run.Disabled,
		Reenabled:  run.Reenabled,
		Error:      run.ErrorMessage,
	}

	if run.ActorUserID != nil && a.db != nil {
		var actor model.User
		if err := a.db.WithContext(g.Request.Context()).
			Select("username").Where("id = ?", *run.ActorUserID).
			First(&actor).Error; err == nil {
			resp.ActorUsername = actor.Username
		}
	}
	return resp
}

// newProbeResponse converts a probe result to its wire shape.
func newProbeResponse(result *service.ProbeResult) webtypes.LDAPProbeResponse {
	resp := webtypes.LDAPProbeResponse{
		BaseDN:                result.BaseDN,
		FilterSent:            result.FilterSent,
		Mode:                  string(result.Mode),
		Attributes:            orEmpty(result.Attributes),
		Matched:               result.Matched,
		ElapsedMS:             int(result.Elapsed.Milliseconds()),
		TimeoutMS:             int(result.Timeout.Milliseconds()),
		TLSInsecureSkipVerify: result.TLSInsecureSkipVerify,
		// The guarantee is part of the response, not only of the
		// documentation. There is no code path that sets this true.
		Wrote: false,
	}

	if result.Entry != nil {
		entry := &webtypes.LDAPProbeEntry{DN: result.Entry.DN}
		for _, attr := range result.Entry.Attributes {
			entry.Attributes = append(entry.Attributes, webtypes.LDAPProbeAttribute{
				Name:            attr.Name,
				Values:          orEmpty(attr.Values),
				Configured:      attr.Configured,
				TruncatedValues: attr.TruncatedValues,
			})
		}
		entry.Attributes = orEmpty(entry.Attributes)
		resp.Entry = entry
	}

	for _, field := range result.Fields {
		out := webtypes.LDAPProbeField{
			Name:             field.Name,
			Attribute:        field.Attribute,
			AttributePresent: field.AttributePresent,
			AttributeValues:  field.AttributeValues,
			Values:           orEmpty(field.Values),
			Error:            field.Error,
		}
		for _, search := range field.Searches {
			out.Searches = append(out.Searches, webtypes.LDAPProbeSearch{
				Name:       search.Name,
				BaseDN:     search.BaseDN,
				FilterSent: search.FilterSent,
				Value:      search.Value,
				Entries:    search.Entries,
				Values:     search.Values,
				Error:      search.Error,
			})
		}
		resp.Fields = append(resp.Fields, out)
	}

	for _, merge := range result.Merge {
		resp.Merge = append(resp.Merge, webtypes.LDAPProbeMerge{
			Name:    merge.Name,
			Action:  merge.Action,
			Kept:    merge.Kept,
			Dropped: merge.Dropped,
			Note:    merge.Note,
		})
	}

	for _, suggestion := range result.Suggestions {
		resp.Suggestions = append(resp.Suggestions, webtypes.LDAPProbeSuggestion{
			Reason: suggestion.Reason,
			YAML:   suggestion.YAML,
		})
	}

	return resp
}

// auditRecord records one event when an auditor is wired, on the same terms
// as the admin controller's: a failed insert never fails the action it
// describes.
func (a *ldapAdminController) auditRecord(g *gin.Context, event service.AuditEvent) {
	if a.audit == nil {
		return
	}
	a.audit.Record(g.Request.Context(), event)
}

// auditActor snapshots the calling identity as an event actor, resolving its
// users-row id — which is also the actor the sync run row records.
func (a *ldapAdminController) auditActor(g *gin.Context) *service.AuditSubject {
	identity, ok := middleware.Identity(g)
	if !ok || identity == nil {
		// not covered: every route here sits behind SessionAuthMiddleware,
		// which aborts before the handler when there is no identity.
		return nil
	}
	var user model.User
	if a.db != nil {
		_ = a.db.WithContext(g.Request.Context()). //nolint:errcheck // a missing row leaves the id empty; the event is still worth recording.
								Select("id").Where("subject = ?", identity.Subject).
								First(&user).Error
	}
	return service.AuditSubjectFromIdentity(identity, user.ID)
}
