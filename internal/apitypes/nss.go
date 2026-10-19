package apitypes

// The NSS directory lookups: GET /api/nss/user/name/:name,
// /api/nss/user/uid/:uid, /api/nss/group/name/:name and
// /api/nss/group/gid/:gid. A host's ssoossh-nss daemon calls them to
// resolve a POSIX name or id, authenticated by a fleet key rather than a
// session. Every response is wrapped in Envelope like the rest of the API.
// See docs/proposals/nss-directory/1-server-api.md.

// Fleet key headers. Every /api/nss/* request carries both, and no other
// request carries either. The id is fixed for the key's life and may be
// logged; the secret must never reach a log line, an audit payload or an
// error message (internal/tracelog redacts it from traces).
const (
	// HeaderNSSKeyID names the fleet key making the request.
	HeaderNSSKeyID = "X-Ssoossh-Key-Id"
	// HeaderNSSKeySecret carries the fleet key's secret.
	HeaderNSSKeySecret = "X-Ssoossh-Key-Secret"
)

// Error codes carried on Envelope.ErrorCode by the /api/nss/* routes only.
// They are separate from the general ErrorCode* set because the daemon acts
// on the difference: nss_not_found is an authoritative miss it may
// negative-cache, while nss_bad_request and nss_disabled are not.
const (
	// ErrorCodeNSSNotFound means no user or group has that name or id
	// (404 Not Found). The only code a host may cache as a miss.
	ErrorCodeNSSNotFound = "nss_not_found"
	// ErrorCodeNSSBadRequest means the name or id failed validation
	// (400 Bad Request), so the server never looked it up.
	ErrorCodeNSSBadRequest = "nss_bad_request"
	// ErrorCodeNSSDisabled means nss.enabled is false on the server
	// (404 Not Found). It says nothing about whether the name exists.
	ErrorCodeNSSDisabled = "nss_disabled"
	// ErrorCodeNSSUnauthorized means the fleet key is missing, unknown,
	// revoked, expired or wrong (401 Unauthorized). The body and timing are
	// the same for every one of those causes.
	ErrorCodeNSSUnauthorized = "nss_unauthorized"
	// ErrorCodeNSSRateLimited means the (key id, source address) bucket is
	// empty (429 Too Many Requests); the response carries Retry-After.
	ErrorCodeNSSRateLimited = "nss_rate_limited"
	// ErrorCodeNSSForbidden means the connection's source address is not in
	// nss.source_allow_list (403 Forbidden). Checked before the key.
	ErrorCodeNSSForbidden = "nss_forbidden"
)

// Values of NSSUserResponse.Status. The server derives the status at
// response time and never stores it: deleted when the entry is tombstoned,
// else disabled when the linked person or the directory entry is disabled,
// else active.
const (
	// NSSStatusActive is an account served as is.
	NSSStatusActive = "active"
	// NSSStatusDisabled is an account the host serves locked, with the
	// server's disabled shell.
	NSSStatusDisabled = "disabled"
	// NSSStatusDeleted is a tombstoned account, served like a disabled one
	// until nss.tombstone_retention ends and then not at all.
	NSSStatusDeleted = "deleted"
)

// NSSUserResponse is the user lookup's response body, by name or by uid.
type NSSUserResponse struct {
	// Name is the login name, stored verbatim from the directory and
	// matched byte for byte.
	Name string `json:"name" validate:"required"`
	// UID is the user id. Frozen once stored: a changed directory value is
	// held as a pending change until an admin accepts it.
	UID uint32 `json:"uid"`
	// GID is the primary group id, after nss.auto_private_groups.
	GID uint32 `json:"gid"`
	// Groups is every membership, primary first, with the gid and the name
	// together, so one user lookup answers both "which groups" and "what is
	// group N called".
	Groups []NSSMembership `json:"groups" validate:"required"`
	// Home is the home directory: the directory attribute, else
	// nss.default_home expanded, else null.
	Home *string `json:"home"`
	// Shell is the login shell path: the directory attribute, else
	// nss.default_shell, else null. For a disabled or deleted account it is
	// nss.disabled_shell.
	Shell *string `json:"shell"`
	// Gecos is the rendered and sanitised nss.gecos_template output.
	Gecos string `json:"gecos"`
	// Status is one of the NSSStatus* values.
	Status string `json:"status" validate:"required"`
	// KeyID is the fleet key that made the request, echoed for log
	// correlation.
	KeyID string `json:"key_id" validate:"required"`
}

// NSSMembership is one entry in NSSUserResponse.Groups.
type NSSMembership struct {
	// GID is the group id.
	GID uint32 `json:"gid"`
	// Name is the group name, matched byte for byte like a user name.
	Name string `json:"name" validate:"required"`
}

// NSSGroupResponse is the group lookup's response body, by name or by gid.
type NSSGroupResponse struct {
	// Name is the group name.
	Name string `json:"name" validate:"required"`
	// GID is the group id, frozen once stored like NSSUserResponse.UID.
	GID uint32 `json:"gid"`
	// Members are the member user names, capped at nss.max_group_members.
	Members []string `json:"members" validate:"required"`
	// MembersTruncated is true when the cap cut Members short, so a host
	// can log that the list is incomplete rather than serve it as whole.
	MembersTruncated bool `json:"members_truncated"`
}
