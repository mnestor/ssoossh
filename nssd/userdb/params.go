package userdb

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/mnestor/ssoossh/internal/varlink"
)

// maxValidID is the largest uid or gid systemd accepts: 2^32-1 is
// UID_INVALID, and nothing above it fits uid_t.
const maxValidID = math.MaxUint32 - 1

// userParams are GetUserRecord's parameters.
type userParams struct {
	UID             *int64          `json:"uid"`
	UserName        *string         `json:"userName"`
	FuzzyNames      json.RawMessage `json:"fuzzyNames"`
	DispositionMask []string        `json:"dispositionMask"`
	UIDMin          *int64          `json:"uidMin"`
	UIDMax          *int64          `json:"uidMax"`
	UUID            json.RawMessage `json:"uuid"`
	Service         *string         `json:"service"`
}

// groupParams are GetGroupRecord's parameters.
type groupParams struct {
	GID             *int64          `json:"gid"`
	GroupName       *string         `json:"groupName"`
	FuzzyNames      json.RawMessage `json:"fuzzyNames"`
	DispositionMask []string        `json:"dispositionMask"`
	GIDMin          *int64          `json:"gidMin"`
	GIDMax          *int64          `json:"gidMax"`
	UUID            json.RawMessage `json:"uuid"`
	Service         *string         `json:"service"`
}

// membershipParams are GetMemberships's parameters.
type membershipParams struct {
	UserName  *string `json:"userName"`
	GroupName *string `json:"groupName"`
	Service   *string `json:"service"`
}

// refreshParams are RefreshUser's parameters.
type refreshParams struct {
	UserName *string `json:"userName"`
	Service  *string `json:"service"`
}

// decodeParams unmarshals a call's parameters into p, or returns the
// InvalidParameter result naming the field that did not decode. Unknown
// fields are ignored, so a newer systemd's additions do not break calls.
func decodeParams(call varlink.Call, p any) (result, bool) {
	err := json.Unmarshal(call.Parameters, p)
	if err == nil {
		return result{}, true
	}
	field := ""
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field = typeErr.Field
	}
	return invalidParameter(field), false
}

// invalidParameter is org.varlink.service.InvalidParameter for one field.
func invalidParameter(field string) result {
	return failure(varlink.ErrorInvalidParameter, map[string]string{"parameter": field})
}

// checkService answers BadService unless service names this one.
func checkService(service *string) (result, bool) {
	if service == nil || *service != ServiceName {
		return failure(ErrorBadService, nil), false
	}
	return result{}, true
}

// present reports whether an optional parameter was sent with a value.
func present(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

// id validates an optional uid or gid parameter.
func id(field string, v *int64) (uint32, bool, result, bool) {
	if v == nil {
		return 0, false, result{}, true
	}
	if *v < 0 || *v > maxValidID {
		return 0, false, invalidParameter(field), false
	}
	return uint32(*v), true, result{}, true
}

// userKey names a user lookup: by uid or by name.
type userKey struct {
	name  string
	uid   uint32
	byUID bool
}

// String is the key's singleflight, refresh and negative-cache key.
func (k userKey) String() string {
	if k.byUID {
		return "user/uid/" + strconv.FormatUint(uint64(k.uid), 10)
	}
	return "user/name/" + k.name
}

// parseUserKey reverses userKey.String.
func parseUserKey(s string) (userKey, bool) {
	if name, ok := strings.CutPrefix(s, "user/name/"); ok {
		return userKey{name: name}, true
	}
	if v, ok := strings.CutPrefix(s, "user/uid/"); ok {
		uid, err := strconv.ParseUint(v, 10, 32)
		return userKey{uid: uint32(uid), byUID: true}, err == nil
	}
	return userKey{}, false
}

// groupKey names a group lookup: by gid or by name.
type groupKey struct {
	name  string
	gid   uint32
	byGID bool
}

// String is the key's singleflight, refresh and negative-cache key.
func (k groupKey) String() string {
	if k.byGID {
		return "group/gid/" + strconv.FormatUint(uint64(k.gid), 10)
	}
	return "group/name/" + k.name
}

// parseGroupKey reverses groupKey.String.
func parseGroupKey(s string) (groupKey, bool) {
	if name, ok := strings.CutPrefix(s, "group/name/"); ok {
		return groupKey{name: name}, true
	}
	if v, ok := strings.CutPrefix(s, "group/gid/"); ok {
		gid, err := strconv.ParseUint(v, 10, 32)
		return groupKey{gid: uint32(gid), byGID: true}, err == nil
	}
	return groupKey{}, false
}
