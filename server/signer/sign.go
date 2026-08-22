package signer

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
)

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
// User and PAM both map to ssh.UserCert: a PAM certificate authenticates a
// person to a local operation (e.g. `sudo`), same as a user certificate
// authenticates a person to an SSH session — the difference is entirely in
// lifetime, options, and who validates it, not in certificate type. Host and
// service are deferred until the user path is fully working (see
// docs/signing-pipeline.md). Service requests never reach
// the sign queue at all: approving one creates an enrollment instead (see
// CertRequestService.Approve).
func certTypeFor(t model.CertificateType) (uint32, error) {
	if t == model.CertificateTypeUser || t == model.CertificateTypePAM {
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
// It's a pure function of (job, ks, fipsEnabled): no database, no
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
func Sign(ctx context.Context, ks CAKeySource, job certmsg.SigningJob, fipsEnabled bool) (certmsg.SignedReply, error) {
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
