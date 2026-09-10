package service

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
)

// HostContext is the rest of what a PAM or console module reports about the
// process and machine asking, beyond the four fields NewCertRequestParams
// carries directly (Hostname, PAMService, TTY, RemoteHost). Every value is
// self-reported by an unauthenticated caller; see apitypes.PAMRequestBody
// for what each one means and where the module reads it.
type HostContext struct {
	RequestingUser        string
	Process               string
	CallerUID             *int64
	CallerGID             *int64
	CallerPID             *int64
	CallerPPID            *int64
	MachineID             string
	OS                    string
	Client                string
	Mode                  string
	ClientTime            *time.Time
	TrustedCAFingerprints []string

	// ClientPolicy is the administrative policy the requesting machine says
	// is in force on it. Nil when it said nothing, which is every request
	// from a machine with no MDM, Group Policy or enforce file — and every
	// request from a client older than the field.
	//
	// Same trust as everything above it: self-reported by an
	// unauthenticated caller and shown as a claim. It is advisory by
	// design, never an input to whether a certificate is issued. See
	// apitypes.ClientPolicy.
	ClientPolicy *apitypes.ClientPolicy
}

// maxTrustedCAFingerprints bounds the fingerprint list the same way
// maxContextFieldLen bounds each string: a caller cannot write arbitrary
// volume into the row.
const maxTrustedCAFingerprints = 8

// maxClientPolicyExtensions bounds the asserted forbidden list on the same
// terms as maxTrustedCAFingerprints: a caller may not write arbitrary volume
// into the row by claiming a very long policy.
const maxClientPolicyExtensions = 32

// applyHostContext copies hc onto req, bounding every string and the
// fingerprint list on the way in and encoding the list as JSON. Bounded
// here, once, for the same reason truncateContextField exists: this is
// persisted, shown on the approval page and kept for audit, so it is
// cleaned where it enters rather than at each of those.
func applyHostContext(req *model.CertificateRequest, hc HostContext) error {
	req.RequestingUser = truncateContextField(hc.RequestingUser)
	req.Process = truncateContextField(hc.Process)
	req.CallerUID = hc.CallerUID
	req.CallerGID = hc.CallerGID
	req.CallerPID = hc.CallerPID
	req.CallerPPID = hc.CallerPPID
	req.MachineID = truncateContextField(hc.MachineID)
	req.OS = truncateContextField(hc.OS)
	req.Client = truncateContextField(hc.Client)
	req.ClientMode = truncateContextField(hc.Mode)
	req.ClientTime = hc.ClientTime

	fingerprints := hc.TrustedCAFingerprints
	if len(fingerprints) > maxTrustedCAFingerprints {
		fingerprints = fingerprints[:maxTrustedCAFingerprints]
	}
	if len(fingerprints) == 0 {
		req.TrustedCAFingerprints = ""
		// Not a bare return: the asserted policy is independent of the
		// fingerprint list, and a request that pins no CA — which is most
		// of them — must still record what its machine claimed.
		return applyClientPolicy(req, hc.ClientPolicy)
	}
	bounded := make([]string, 0, len(fingerprints))
	for _, fp := range fingerprints {
		bounded = append(bounded, truncateContextField(fp))
	}
	encoded, err := json.Marshal(bounded)
	if err != nil {
		// not covered: a []string, so json.Marshal cannot fail.
		return fmt.Errorf("failed to encode trusted CA fingerprints: %w", err)
	}
	req.TrustedCAFingerprints = string(encoded)

	return applyClientPolicy(req, hc.ClientPolicy)
}

// applyClientPolicy encodes the requesting machine's asserted policy onto
// req, bounding each name the same way every other self-reported string is
// bounded. Absent policy leaves the column empty rather than storing "null",
// so "said nothing" and "said nothing forbidden" read alike downstream.
func applyClientPolicy(req *model.CertificateRequest, policy *apitypes.ClientPolicy) error {
	if policy == nil || (len(policy.ForbiddenExtensions) == 0 && policy.FIPS == nil) {
		req.ClientPolicy = ""
		return nil
	}

	bounded := &apitypes.ClientPolicy{FIPS: policy.FIPS}
	forbidden := policy.ForbiddenExtensions
	if len(forbidden) > maxClientPolicyExtensions {
		forbidden = forbidden[:maxClientPolicyExtensions]
	}
	for _, ext := range forbidden {
		bounded.ForbiddenExtensions = append(bounded.ForbiddenExtensions, truncateContextField(ext))
	}

	encoded, err := json.Marshal(bounded)
	if err != nil {
		// not covered: strings and a *bool, so json.Marshal cannot fail.
		return fmt.Errorf("failed to encode client policy: %w", err)
	}
	req.ClientPolicy = string(encoded)
	return nil
}

// decodeClientPolicy reads the asserted policy back. An empty or unreadable
// column reads as no assertion rather than an error, for the same reason
// decodeTrustedCAFingerprints does: this is display context, not an input to
// any decision, so a malformed one must not fail an approval page.
func decodeClientPolicy(encoded string) *apitypes.ClientPolicy {
	if encoded == "" {
		return nil
	}
	var policy apitypes.ClientPolicy
	if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
		return nil
	}
	return &policy
}

// decodeTrustedCAFingerprints reads the JSON list back. An empty or
// unreadable column reads as no fingerprints rather than an error: this is
// display context, not an input to any decision.
func decodeTrustedCAFingerprints(encoded string) []string {
	if encoded == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		return nil
	}
	return out
}

// hostContextDetail is the compact set of host-context keys every cert.*
// event carries, so a reviewer never has to join back to cert.requested to
// learn which account, machine, terminal and command a decision was about.
// username and hostname come through ReportedIdentity, so a user-type
// event carries the local client's identity rather than empty PAM columns.
//
// Every key is present on every event, empty or not, so log consumers see
// one shape regardless of certificate type.
func hostContextDetail(req model.CertificateRequest) map[string]any {
	username, hostname := req.ReportedIdentity()
	return map[string]any{
		"username":        username,
		"hostname":        hostname,
		"pam_service":     req.PAMService,
		"tty":             req.TTY,
		"remote_host":     req.RemoteHost,
		"requesting_user": req.RequestingUser,
		"process":         req.Process,
		"machine_id":      req.MachineID,
		"client":          req.Client,
	}
}

// fullHostContextDetail is hostContextDetail plus the long tail that only
// cert.requested records: the process identifiers, the platform, the
// module's configured mode, its clock, and the CA fingerprints it trusts.
func fullHostContextDetail(req model.CertificateRequest) map[string]any {
	d := hostContextDetail(req)
	d["local_username"] = req.LocalUsername
	d["local_hostname"] = req.LocalHostname
	d["caller_uid"] = req.CallerUID
	d["caller_gid"] = req.CallerGID
	d["caller_pid"] = req.CallerPID
	d["caller_ppid"] = req.CallerPPID
	d["os"] = req.OS
	d["client_mode"] = req.ClientMode
	d["client_time"] = req.ClientTime
	d["trusted_ca_fingerprints"] = decodeTrustedCAFingerprints(req.TrustedCAFingerprints)
	return d
}

// withDetail merges extra onto base and returns base, so an event can be
// built as "the host context, plus what this stage adds" without each call
// site re-listing the context keys.
func withDetail(base map[string]any, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}
