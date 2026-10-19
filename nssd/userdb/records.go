package userdb

import (
	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/nssd/policy"
)

// dispositionRegular is every record's disposition: a person or shared
// account in a directory, never a system or dynamic user.
const dispositionRegular = "regular"

// userRecord is a JSON user record (systemd docs/USER_RECORD.md), only the
// fields Part 3's mapping table lists. No memberOf: GetMemberships is
// authoritative.
type userRecord struct {
	UserName      string      `json:"userName"`
	UID           uint32      `json:"uid"`
	GID           uint32      `json:"gid"`
	RealName      string      `json:"realName,omitempty"`
	HomeDirectory string      `json:"homeDirectory"`
	Shell         string      `json:"shell"`
	Disposition   string      `json:"disposition"`
	Locked        bool        `json:"locked,omitempty"`
	Service       string      `json:"service"`
	Privileged    *privileged `json:"privileged,omitempty"`
}

// privileged is the record's privileged section, which the userdb API
// lets only uid 0 see.
type privileged struct {
	// HashedPassword is ["*"]: nss-systemd copies the first entry into
	// sp_pwdp, and "*" means no password login while OpenSSH's UsePAM no
	// path, which treats only a "!" prefix as locked, still lets a
	// certificate in (README R27, U7).
	HashedPassword []string `json:"hashedPassword"`
}

// groupRecord is a JSON group record. No members: nss-systemd fetches
// them with GetMemberships.
type groupRecord struct {
	GroupName   string `json:"groupName"`
	GID         uint32 `json:"gid"`
	Disposition string `json:"disposition"`
	Service     string `json:"service"`
}

// recordReply is GetUserRecord's and GetGroupRecord's reply.
type recordReply struct {
	Record any `json:"record"`
	// Incomplete says the record lacks its privileged section, which is
	// the case for every caller but uid 0.
	Incomplete bool `json:"incomplete"`
}

// membershipReply is one GetMemberships reply.
type membershipReply struct {
	UserName  string `json:"userName"`
	GroupName string `json:"groupName"`
}

// infoReply is org.varlink.service.GetInfo's reply.
type infoReply struct {
	Vendor     string   `json:"vendor"`
	Product    string   `json:"product"`
	Version    string   `json:"version"`
	URL        string   `json:"url"`
	Interfaces []string `json:"interfaces"`
}

// userRecordFor maps a user after policy to its record. The privileged
// section goes only to a uid 0 peer (README R27); everyone else gets the
// record marked incomplete.
func userRecordFor(u policy.User, peer Peer) recordReply {
	rec := userRecord{
		UserName:      u.Name,
		UID:           u.UID,
		GID:           u.GID,
		RealName:      u.RealName,
		HomeDirectory: u.Home,
		Shell:         u.Shell,
		Disposition:   dispositionRegular,
		Locked:        u.Locked,
		Service:       ServiceName,
	}
	if peer.UID == 0 {
		rec.Privileged = &privileged{HashedPassword: []string{"*"}}
		return recordReply{Record: rec}
	}
	return recordReply{Record: rec, Incomplete: true}
}

// groupRecordFor maps a group after policy to its record.
func groupRecordFor(g policy.Group) recordReply {
	return recordReply{Record: groupRecord{
		GroupName:   g.Name,
		GID:         g.GID,
		Disposition: dispositionRegular,
		Service:     ServiceName,
	}}
}

// info is the GetInfo reply. Introspection is not implemented, as in
// systemd's own userdb services, so the interface list is all there is.
func info() infoReply {
	return infoReply{
		Vendor:     "ssoossh",
		Product:    "ssoossh-nss",
		Version:    version.Version,
		URL:        version.Github,
		Interfaces: []string{InterfaceVarlink, InterfaceUserDatabase, InterfaceNSS},
	}
}
