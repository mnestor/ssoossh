// Package auditns names the namespaces of the audit taxonomy: the part of
// an action's name before the first dot (server/service/audit_actions.go).
//
// It is its own package because tygo maps one Go package to one TypeScript
// file, and the web UI walks every constant in the generated taxonomy as an
// action. Generating the namespaces from here gives them their own file,
// frontend/src/lib/api/generated/auditnamespaces.ts, which the audit page's
// action filter groups by. Keep other declarations out of this package: tygo
// would emit them too.
package auditns

// Namespace is the subject area an audit action belongs to.
type Namespace string

// Every action's name starts with one of these and a dot. A test in
// server/service fails for an action that does not, and a web UI test fails
// for a namespace without a label in the action filter.
const (
	Auth       Namespace = "auth"
	Cert       Namespace = "cert"
	Enrollment Namespace = "enrollment"
	User       Namespace = "user"
	LDAP       Namespace = "ldap"
	Admin      Namespace = "admin"
	NSS        Namespace = "nss"
)
