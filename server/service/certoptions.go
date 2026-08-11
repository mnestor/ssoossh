package service

import "github.com/mnestor/ssoossh/server/certmsg"

// RequestedOptions are the client-supplied certificate options carried on a
// CertificateRequest, narrowed against server config (config.CertOptionsUser
// / CertOptionsService / CertOptions) before anything reaches the web UI or
// gets signed — see root CLAUDE.md Hard Constraints ("server config is the
// outer bound").
//
// Aliased to certmsg.RequestedOptions rather than defined here: the signing
// job carries these across the queue to a component that must not import
// this package (see the certmsg package doc), and the two must be the same
// type rather than two structs kept in sync by hand.
type RequestedOptions = certmsg.RequestedOptions
