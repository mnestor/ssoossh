package service

import (
	"fmt"
	"net/netip"

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

	// clientTimeout is this type's whole request-timing budget, already
	// resolved: the type's own cert_options.<type>.client_timeout when it
	// sets one, otherwise the global cert_options.client_timeout. Config
	// guarantees it never exceeds the global (see
	// config.CertificateOptions.Validate), which is what lets everything
	// derived from the global — the stranded sweep's cutoff, the
	// resolved-outcome cache's eviction age, the sweep interval — stay
	// correct for a request on a shorter budget.
	clientTimeout time.Duration

	// allowedNetworks refuses request creation from outside these
	// networks, or is empty for no gate. Only the console type configures
	// one: the server's half of per-host policy has to rest on the source
	// address, which the server observes, rather than a hostname an
	// unauthenticated caller typed.
	allowedNetworks []netip.Prefix

	// usesUserCode marks a type whose requests carry a short code a human
	// types into the web UI instead of opening a URL the client printed
	// (docs/proposals/console-login-pam.md). Console only.
	usesUserCode bool
}

// approvalTTL is how long a request of this type stays approvable, the
// human's share of the type's own budget. See
// config.CertificateOptions.ApprovalTTL for the split and why the global
// budget stays the ceiling.
func (p *certTypePolicy) approvalTTL() time.Duration {
	return config.ApprovalTTLFor(p.clientTimeout)
}

// permitsSource reports whether a request from sourceIP may be created for
// this type. An empty allowedNetworks permits everything, which is the
// default for every type.
//
// An unparseable or empty source address is refused whenever a gate is
// configured. That is the opposite of matchSourceRule's err-on-the-side-of-
// permissive handling, deliberately: there the address only tiers a
// lifetime, here it is the whole of the gate, and a gate that opens when it
// cannot tell where a request came from is not a gate.
func (p *certTypePolicy) permitsSource(sourceIP string) bool {
	if len(p.allowedNetworks) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(sourceIP)
	if err != nil {
		return false
	}
	// ::ffff:10.0.0.1 matches 10.0.0.0/8, the same normalization the
	// lifetime policy's source rules apply.
	addr = addr.Unmap()
	for _, n := range p.allowedNetworks {
		if n.Contains(addr) {
			return true
		}
	}
	return false
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
	requireConsole, err := parseRequire(opts.Console.Require, "cert_options.console.require", declaredClaims)
	if err != nil {
		return nil, err
	}
	consoleNetworks, err := parseAllowedNetworks(opts.Console.AllowedNetworks, "cert_options.console.allowed_networks")
	if err != nil {
		return nil, err
	}
	// Unset inherits the global budget; config has already refused a value
	// longer than it.
	consoleTimeout := opts.Console.ClientTimeout
	if consoleTimeout <= 0 {
		consoleTimeout = opts.ClientTimeout
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

	// localAuthPrincipals returns the approver's selection for PAM and
	// console requests, or every account they hold when nothing was
	// selected. The module names one local account; pam_ssoossh's check 3
	// (pam_ssoossh/checks.go) then matches the certificate's principals
	// against it, directly or through the host's principals-map. The
	// approver picks which of their accounts to put in the certificate for
	// that match; the all-held default keeps direct API callers that send
	// no selection working, and the server never models host-local state
	// either way (docs/internals/design-brief.md, "Principal mapping":
	// nothing syncs the mapping down).
	localAuthPrincipals := func(identity *Identity, selected []string) []string {
		if len(selected) > 0 {
			return selected
		}
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
			clientTimeout: opts.ClientTimeout,
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
			clientTimeout:      opts.ClientTimeout,
			linkage: func(identity *Identity, selection ApprovalSelection) error {
				return checkServiceAccountLinkage(identity, selection.ServiceAccount)
			},
		},
		model.CertificateTypePAM: {
			require:       requirePAM,
			validDuration: opts.PAM.ValidDuration,
			extensions:    opts.PAM.Extensions,
			keyIDTemplate: kt.pam,
			principals:    localAuthPrincipals,
			flow:          flowSigning,
			clientTimeout: opts.ClientTimeout,
			// The approver picks the principals, as for the user type, so the
			// selection has to be checked against what they hold.
			linkage: func(identity *Identity, selection ApprovalSelection) error {
				return checkUserPrincipalLinkage(identity, selection.Principals)
			},
		},
		model.CertificateTypeConsole: {
			require:       requireConsole,
			validDuration: opts.Console.ValidDuration,
			extensions:    opts.Console.Extensions,
			keyIDTemplate: kt.console,
			// Same reasoning as PAM, and it matters more here: the module
			// names the account typed at the `login:` prompt, and the
			// certificate names accounts the approver holds instead. An
			// attacker who types `root` at an unattended console gets a
			// certificate check 3 refuses unless the approver holds root or
			// the host's principals-map already says they may become it.
			principals:      localAuthPrincipals,
			flow:            flowSigning,
			clientTimeout:   consoleTimeout,
			allowedNetworks: consoleNetworks,
			usesUserCode:    true,
			linkage: func(identity *Identity, selection ApprovalSelection) error {
				return checkUserPrincipalLinkage(identity, selection.Principals)
			},
		},
	}, nil
}

// parseAllowedNetworks parses a type's allowed_networks list. label names
// the config key in errors. config.CertificateOptions.Validate has already
// rejected an unparseable entry at startup; this repeats the parse because
// it is what produces the values, and a second error message from the same
// input costs nothing.
func parseAllowedNetworks(cidrs []string, label string) ([]netip.Prefix, error) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for i, c := range cidrs {
		prefix, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: invalid CIDR %q: %w", label, i, c, err)
		}
		out = append(out, prefix)
	}
	return out, nil
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
