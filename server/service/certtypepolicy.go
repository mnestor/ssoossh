package service

import (
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
	requireGroup  string
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
	// principals computes a certificate's principal list from per-request
	// context and the approver's selection (for user-type requests). For
	// user-type requests, returns the selection (or defaults to
	// []string{identity.Username} if selection is empty). For PAM requests,
	// ignores both identity and selection, returning the local account being
	// authenticated (req.Username at call-time). Service certificates ignore
	// this field and use the selected service account directly. See
	// docs/internals/design-brief.md for the "Which LDAP attributes become
	// principals" open question.
	principals func(pamUsername string, identity *Identity, selected []string) []string
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
// table keyed by model.CertificateType.
func newCertTypePolicies(opts config.CertificateOptions, kt *keyIDTemplates) map[model.CertificateType]*certTypePolicy {
	// userPrincipals returns the approver's selection for user-type
	// requests, or defaults to the approver's username if the selection is
	// empty (preserving existing behavior for direct API callers).
	userPrincipals := func(_ string, identity *Identity, selected []string) []string {
		if len(selected) > 0 {
			return selected
		}
		return []string{identity.Username}
	}

	// servicePrincipals is the placeholder for service certificates — this
	// field is never consulted for service types (approveServiceEnrollment
	// uses selection.ServiceAccount directly), but it exists for symmetry.
	servicePrincipals := func(_ string, identity *Identity, _ []string) []string {
		return []string{identity.Username}
	}

	return map[model.CertificateType]*certTypePolicy{
		model.CertificateTypeUser: {
			requireGroup:  opts.User.RequireGroup,
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
			requireGroup:       opts.Service.RequireGroup,
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
			requireGroup:  opts.PAM.RequireGroup,
			validDuration: opts.PAM.ValidDuration,
			extensions:    opts.PAM.Extensions,
			keyIDTemplate: kt.pam,
			principals: func(pamUsername string, _ *Identity, _ []string) []string {
				return []string{pamUsername}
			},
			flow: flowSigning,
		},
	}
}
