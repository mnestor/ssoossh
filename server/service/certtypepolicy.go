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
	extensions    []string
	// noTouchEligible is only true for CertificateTypeService — see
	// CertRequestService.Approve's doc comment.
	noTouchEligible bool
	keyIDTemplate   *template.Template
	// principals computes a certificate's principal list from per-request
	// context. See docs/dev/ssoossh-context.md's "Which LDAP attributes become
	// principals" open question for why this defaults to the approver's
	// identity, and CertRequestService.Approve's doc comment for PAM/Host's
	// exceptions.
	principals func(hostname, pamUsername string, identity *Identity) []string
	flow       certApprovalFlow
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
	// identityUsername is shared by User and Service — both fall back to
	// the approver's own identity (see resolvePrincipals' old doc comment).
	identityUsername := func(_, _ string, identity *Identity) []string {
		return []string{identity.Username}
	}

	return map[model.CertificateType]*certTypePolicy{
		model.CertificateTypeUser: {
			requireGroup:  opts.User.RequireGroup,
			validDuration: opts.User.ValidDuration,
			extensions:    opts.User.Extensions,
			keyIDTemplate: kt.user,
			principals:    identityUsername,
			flow:          flowSigning,
		},
		model.CertificateTypeService: {
			requireGroup:    opts.Service.RequireGroup,
			validDuration:   opts.Service.ValidDuration,
			extensions:      opts.Service.Extensions,
			noTouchEligible: true,
			keyIDTemplate:   kt.service,
			principals:      identityUsername,
			flow:            flowEnrollment,
		},
		model.CertificateTypeHost: {
			requireGroup:  opts.Host.RequireGroup,
			validDuration: opts.Host.ValidDuration,

			keyIDTemplate: kt.host,
			principals: func(hostname, _ string, _ *Identity) []string {
				return []string{hostname}
			},
			flow: flowSigning,
		},
		model.CertificateTypePAM: {
			requireGroup:  opts.PAM.RequireGroup,
			validDuration: opts.PAM.ValidDuration,
			extensions:    opts.PAM.Extensions,
			keyIDTemplate: kt.pam,
			principals: func(_, pamUsername string, _ *Identity) []string {
				return []string{pamUsername}
			},
			flow: flowSigning,
		},
	}
}
