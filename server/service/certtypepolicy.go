package service

import (
	"fmt"
	// Key ID templates render plain-text SSH certificate key IDs, never
	// HTML; html/template's escaping would corrupt them.
	"text/template" // nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// certApprovalFlow is how Approve routes an approved request for its
// certificate type. See certTypePolicy.flow.
type certApprovalFlow int

const (
	// flowUnsupported is the zero value on purpose: a policy that never sets
	// an explicit flow fails closed here rather than silently falling into a
	// real issuance path.
	flowUnsupported certApprovalFlow = iota //nolint:unused
	// flowEnrollment types create a model.Enrollment instead of queueing a
	// signing job — see CertRequestService.approveServiceEnrollment.
	flowEnrollment
	// flowSigning types are queued to certmsg.SignQueueTopic — see
	// CertRequestService.approveForSigning.
	flowSigning
)

// certTypePolicy is everything about a model.CertificateType that used to
// be re-derived by separate switches in resolveCertOptions,
// resolvePrincipals, keyIDTemplates.execute, and Approve. newCertTypePolicies
// builds one of these per type, once, at construction, instead of every
// caller re-switching on model.CertificateType.
type certTypePolicy struct {
	// require is the parsed cert_options.<type>.require condition gating
	// who may approve this certificate type, or nil for no gate. Evaluated
	// against the approver's identity with its Extra fields hydrated —
	// Approve resolves the users row before the gate runs.
	require       *parsedCondition
	validDuration time.Duration
	// enrollmentDuration is how long a flowEnrollment type's code stays
	// redeemable. Zero for every other type, which mints no code. See
	// config.CertOptionsService.EnrollmentDuration for why it is not
	// derived from validDuration.
	enrollmentDuration time.Duration
	extensions         []string
	// noTouchEligible is only true for CertificateTypeService — see
	// CertRequestService.Approve's doc comment.
	noTouchEligible bool
	keyIDTemplate   *template.Template
	// principals computes a certificate's principal list from the approver's
	// identity and, for user-type requests, their selection. Every type
	// derives it from the approver and nothing else: the certificate asserts
	// who the identity provider vouched for, and the host decides which local
	// accounts that maps to (docs/internals/design-brief.md, "Principal
	// mapping"). That is why this takes no per-request context. A PAM
	// request's req.Username used to be returned here verbatim, which made an
	// unauthenticated caller the author of the one field the certificate is
	// authorized on. See docs/proposals/pam-principal-source.md.
	//
	// User-type requests return the selection, or default to the approver's
	// username when none was made. PAM requests always return every account
	// the approver holds. Service certificates ignore this field and use the
	// selected service account directly.
	principals func(identity *Identity, selected []string) []string
	flow       certApprovalFlow
	// linkage is the per-type check tying a certificate's principals to
	// accounts the approver actually holds, or nil for a type with no such
	// tie. Called by checkApproverAuthorization after the group check.
	linkage func(identity *Identity, selection ApprovalSelection) error
}

// narrowRequestedOptions narrows requested against p's server-config bound.
// See CertRequestService.Approve's doc comment for the policy this
// implements.
func narrowRequestedOptions(p *certTypePolicy, requested RequestedOptions) RequestedOptions {
	return RequestedOptions{
		Extensions:      intersectStrings(requested.Extensions, p.extensions),
		NoTouchRequired: requested.NoTouchRequired && p.noTouchEligible,
	}
}

// newCertTypePolicies resolves opts' per-type options against kt's
// already-parsed key ID templates (see newKeyIDTemplates) into one lookup
// table keyed by model.CertificateType. declaredClaims is
// authentication.fields.extra, validated against each require condition's
// claim references; a bad condition is a startup error.
func newCertTypePolicies(opts config.CertificateOptions, kt *keyIDTemplates, declaredClaims map[string]string) (map[model.CertificateType]*certTypePolicy, error) {
	requireUser, err := parseRequire(opts.User.Require, "cert_options.user.require", declaredClaims)
	if err != nil {
		return nil, err
	}
	requireService, err := parseRequire(opts.Service.Require, "cert_options.service.require", declaredClaims)
	if err != nil {
		return nil, err
	}
	requirePAM, err := parseRequire(opts.PAM.Require, "cert_options.pam.require", declaredClaims)
	if err != nil {
		return nil, err
	}
	// userPrincipals returns the approver's selection for user-type
	// requests, or defaults to the approver's username if the selection is
	// empty (preserving existing behavior for direct API callers).
	userPrincipals := func(identity *Identity, selected []string) []string {
		if len(selected) > 0 {
			return selected
		}
		return []string{identity.Username}
	}

	// servicePrincipals is the placeholder for service certificates — this
	// field is never consulted for service types (approveServiceEnrollment
	// uses selection.ServiceAccount directly), but it exists for symmetry.
	servicePrincipals := func(identity *Identity, _ []string) []string {
		return []string{identity.Username}
	}

	// pamPrincipals returns every account the approver holds. There is no
	// selection to honour: the module names one local account, and the host's
	// own principals-map decides whether these principals authorize it
	// (pam_ssoossh/checks.go, check 3). Listing them all lets the host make
	// that decision without the server modelling host-local state it
	// deliberately does not have (docs/internals/design-brief.md, "Principal
	// mapping": nothing syncs the mapping down).
	pamPrincipals := func(identity *Identity, _ []string) []string {
		return heldAccounts(identity)
	}

	return map[model.CertificateType]*certTypePolicy{
		model.CertificateTypeUser: {
			require:       requireUser,
			validDuration: opts.User.ValidDuration,
			extensions:    opts.User.Extensions,
			keyIDTemplate: kt.user,
			principals:    userPrincipals,
			flow:          flowSigning,
			linkage: func(identity *Identity, selection ApprovalSelection) error {
				return checkUserPrincipalLinkage(identity, selection.Principals)
			},
		},
		model.CertificateTypeService: {
			require:            requireService,
			validDuration:      opts.Service.ValidDuration,
			enrollmentDuration: opts.Service.EnrollmentDuration,
			extensions:         opts.Service.Extensions,
			noTouchEligible:    true,
			keyIDTemplate:      kt.service,
			principals:         servicePrincipals,
			flow:               flowEnrollment,
			linkage: func(identity *Identity, selection ApprovalSelection) error {
				return checkServiceAccountLinkage(identity, selection.ServiceAccount)
			},
		},
		model.CertificateTypePAM: {
			require:       requirePAM,
			validDuration: opts.PAM.ValidDuration,
			extensions:    opts.PAM.Extensions,
			keyIDTemplate: kt.pam,
			principals:    pamPrincipals,
			flow:          flowSigning,
			// No linkage: pamPrincipals returns the approver's own accounts,
			// so there is no selection to cross-check against what they hold.
			// checkUserPrincipalLinkage exists for the user type, where the
			// approver picks the principals.
		},
	}, nil
}

// parseRequire parses one type's require gate, or returns nil for an unset
// one. label names the config key in errors.
func parseRequire(cond *config.PolicyCondition, label string, declaredClaims map[string]string) (*parsedCondition, error) {
	if cond.IsZero() {
		return nil, nil
	}
	parsed, err := parseCondition(cond, declaredClaims, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return parsed, nil
}
