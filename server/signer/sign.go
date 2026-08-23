package signer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	sshcrypto "github.com/mnestor/ssoossh/internal/crypto/ssh"
	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

// SignLimits holds the lifetime ceilings the signer enforces before minting
// certificates, as defense-in-depth: an attacker able to publish directly to
// the sign queue must not get unbounded certificates.
type SignLimits struct {
	MaxCertLifetime        time.Duration
	MaxHostCertLifetime    time.Duration
	MaxServiceCertLifetime time.Duration
}

// signError carries a certmsg.ErrCode* classification alongside the
// human-readable message, so the handler can turn it into a failure reply
// the client sees as a terminal outcome rather than a hang.
type signError struct {
	code string
	err  error
}

func (e *signError) Error() string { return e.err.Error() }
func (e *signError) Unwrap() error { return e.err }

func newSignError(code string, format string, args ...any) *signError {
	return &signError{code: code, err: fmt.Errorf(format, args...)}
}

// errorCode returns err's certmsg.ErrCode* classification, defaulting to
// ErrCodeSignFailed for anything unclassified.
func errorCode(err error) string {
	var se *signError
	if errors.As(err, &se) {
		return se.code
	}
	return certmsg.ErrCodeSignFailed
}

// certTypeFor maps a ssoossh certificate type onto an SSH certificate type.
//
// User, PAM, and service all map to ssh.UserCert: a PAM certificate
// authenticates a person to a local operation (e.g. `sudo`) and a service
// certificate authenticates an unattended account to an SSH session, same
// as a user certificate authenticates a person — the differences are
// entirely in lifetime, options, and who validates them, not in
// certificate type. Service jobs reach the sign queue from
// EnrollmentService.Retrieve (approval creates the enrollment; each
// redemption publishes a job). Host certificates are not issued.
func certTypeFor(t model.CertificateType) (uint32, error) {
	switch t {
	case model.CertificateTypeUser, model.CertificateTypePAM, model.CertificateTypeService:
		return ssh.UserCert, nil
	}
	return 0, newSignError(certmsg.ErrCodeUnsupportedType, "certificate type %q is not supported yet", t)
}

// parsePublicKey parses the job's authorized_keys-format public key,
// classifying a parse failure so it reaches the client as a terminal
// outcome rather than an unexplained error.
func parsePublicKey(authorizedKey string) (ssh.PublicKey, error) {
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey)) //nolint:dogsled // ParseAuthorizedKey's comment/options/rest returns are irrelevant here.
	if err != nil {
		return nil, newSignError(certmsg.ErrCodeBadPublicKey, "failed to parse public key: %w", err)
	}
	return publicKey, nil
}

// permissionsFor maps the job's already-narrowed options onto SSH
// certificate permissions.
//
// This is a faithful translation, not a policy check: Approve is the only
// policy point (it currently drops ForceCommand/SourceAddresses entirely and
// grants NoTouchRequired only for service certificates, which never reach
// here). Those branches are therefore dead today — but they're implemented
// rather than omitted, because silently dropping an option the job asked for
// would make the issued certificate quietly disagree with what was approved.
func permissionsFor(opts certmsg.RequestedOptions) ssh.Permissions {
	extensions := make(map[string]string, len(opts.Extensions)+1)
	for _, e := range opts.Extensions {
		extensions[e] = ""
	}
	if opts.NoTouchRequired {
		extensions["no-touch-required"] = ""
	}

	criticalOptions := make(map[string]string, 2)
	if opts.ForceCommand != "" {
		criticalOptions["force-command"] = opts.ForceCommand
	}
	if len(opts.SourceAddresses) > 0 {
		criticalOptions["source-address"] = strings.Join(opts.SourceAddresses, ",")
	}

	return ssh.Permissions{
		CriticalOptions: criticalOptions,
		Extensions:      extensions,
	}
}

// Sign produces a signed certificate for job using the CA key from ks.
//
// It's a pure function of (job, ks, fipsEnabled, limits): no database, no
// server-config re-derivation. The returned reply carries what was
// *actually* signed, so the listener/resolver can write the audit row
// straight from it without re-reading and re-interpreting the original
// request.
//
// fipsEnabled repeats the same FIPS-approval check CertRequestService.Approve
// already made, as defense in depth: if the main server process were ever
// compromised, an attacker able to publish directly to the sign queue would
// otherwise bypass that check entirely. It's a plain value, not policy
// re-derivation — see this package's doc comment.
//
// limits enforces the lifetime ceilings as a defense-in-depth backstop:
// it's a plain value passed by bootstrap, not policy re-derivation.
func Sign(ctx context.Context, ks CAKeySource, job certmsg.SigningJob, fipsEnabled bool, limits SignLimits) (certmsg.SignedReply, error) {
	// Validate certificate lifetime as a backstop: an attacker able to
	// publish directly to the sign queue must not get unbounded certificates.
	// This is a plain-value check, not policy re-derivation.
	if job.ValidBefore.Before(job.ValidAfter) || job.ValidBefore.Equal(job.ValidAfter) {
		return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeSignFailed,
			"certificate lifetime invalid: ValidBefore (%v) must be after ValidAfter (%v)", job.ValidBefore, job.ValidAfter)
	}

	span := job.ValidBefore.Sub(job.ValidAfter)
	cap := limits.MaxCertLifetime
	switch job.Type {
	case model.CertificateTypeHost:
		cap = limits.MaxHostCertLifetime
	case model.CertificateTypeService:
		cap = limits.MaxServiceCertLifetime
	}
	if span > cap {
		return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeLifetimeRejected,
			"certificate lifetime %v exceeds maximum %v for type %q", span, cap, job.Type)
	}

	certType, err := certTypeFor(job.Type)
	if err != nil {
		return certmsg.SignedReply{}, err
	}

	publicKey, err := parsePublicKey(job.PublicKey)
	if err != nil {
		return certmsg.SignedReply{}, err
	}

	if fipsEnabled {
		keyType, ok := fipsmode.FromSSHAlgorithm(publicKey.Type())
		if !ok || !fipsmode.IsApprovedInFIPS(keyType) {
			return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeFIPSNotApproved,
				"public key algorithm %q is not FIPS-approved", publicKey.Type())
		}
	}

	// Validate principals as a backstop: these should already be validated in
	// the approval path, but checking here provides defense in depth.
	for _, p := range job.Principals {
		if err := sshcrypto.ValidatePrincipal(p); err != nil {
			return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeSignFailed,
				"invalid principal: %w", err)
		}
	}

	caSigner, err := ks.Signer(ctx)
	if err != nil {
		return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeCAUnavailable, "failed to obtain CA signing key: %w", err)
	}

	// Use the pre-allocated serial from the job. Pre-allocation at
	// approval time avoids burning serials on signing failures.
	serial := job.Serial

	permissions := permissionsFor(job.RequestedOptions)
	cert := &ssh.Certificate{
		Key:             publicKey,
		Serial:          serial,
		CertType:        certType,
		KeyId:           job.KeyID,
		ValidPrincipals: job.Principals,
		//nolint:gosec // Unix timestamps are non-negative; conversion to uint64 is safe.
		ValidAfter: uint64(job.ValidAfter.Unix()),
		//nolint:gosec // Unix timestamps are non-negative; conversion to uint64 is safe.
		ValidBefore: uint64(job.ValidBefore.Unix()),
		Permissions: permissions,
	}

	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		return certmsg.SignedReply{}, newSignError(certmsg.ErrCodeSignFailed, "failed to sign certificate: %w", err)
	}

	return certmsg.SignedReply{
		RequestID:            job.RequestID,
		Type:                 job.Type,
		Certificate:          strings.TrimSpace(string(ssh.MarshalAuthorizedKey(cert))),
		Serial:               serial,
		KeyID:                job.KeyID,
		Principals:           job.Principals,
		Hostname:             job.Hostname,
		PublicKeyFingerprint: ssh.FingerprintSHA256(publicKey),
		CriticalOptions:      permissions.CriticalOptions,
		Extensions:           job.RequestedOptions.Extensions,
		ValidAfter:           job.ValidAfter,
		ValidBefore:          job.ValidBefore,
	}, nil
}
