// Package principalsmap reads a local, hand-authored file mapping a local
// account name to the certificate principals allowed to assume it. It is
// standalone: nothing about it depends on or coordinates with the server's
// host-mapping/sync system — a host admin owns and edits this file directly
// on the machine it applies to.
//
// # Accepted format
//
// The file is YAML, but this package parses a fixed subset of it by hand
// rather than linking a YAML library: pam_ssoossh is a c-shared module
// mapped into sudo and sshd, and gopkg.in/yaml.v3 cost 549 KB of the
// module's size to read a file with exactly one shape. The subset is:
//
//	# a comment
//	alice:            # an account, at the start of the line
//	  - alice         # a principal allowed to assume it
//	  - admin         # indentation is free; "- " is what marks an item
//	bob: [bob, ops]   # a flow sequence is accepted on the account's own line
//	carol:            # an account with no principals at all; "null" and
//	                  # "~" mean the same thing
//
// Values may be wrapped in matching single or double quotes, which are
// stripped. Escape sequences inside them are not interpreted — a quoted
// value containing a backslash, a quote of the same kind, or (inside a flow
// sequence) a comma is rejected rather than guessed at. Anything else the
// wider YAML language allows — nested mappings, anchors, multi-line
// scalars, multiple documents — is an error here.
//
// Rejecting is safe but not free: pam_ssoossh treats a map that fails to
// load as no map at all and falls back to requiring the certificate to
// carry the exact local account name (see checkPrincipal), which is a
// different policy than the file asked for. That is why this parser accepts
// every shape yaml.v3 accepted for a file of this one shape, and errors
// only where yaml.v3 would also have errored.
package principalsmap

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// PrincipalsMap maps a local account name to the certificate principals
// allowed to assume it, e.g.:
//
//	alice:
//	  - alice
//	  - admin
type PrincipalsMap map[string][]string

// LoadFromFile reads and parses a principals map file.
func LoadFromFile(path string) (PrincipalsMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read principals map: %w", err)
	}

	m, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse principals map: %w", err)
	}
	return m, nil
}

// parse reads the accepted subset documented on the package. Errors name
// the line, since the file is hand-edited and the message reaches an
// operator through pam_ssoossh's syslog warning.
func parse(data []byte) (PrincipalsMap, error) {
	m := PrincipalsMap{}

	// account is the account whose list is open — the one a "- principal"
	// line appends to. An inline value (a flow sequence, null) closes its
	// account immediately, so a stray item line after one is an error
	// rather than an append to whatever came before.
	var account string

	for i, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		indent, line, err := contentOf(raw)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if line == "" {
			continue
		}

		if item, ok := strings.CutPrefix(line, "-"); ok {
			if err := appendPrincipal(m, account, item); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			continue
		}

		if account, err = addAccount(m, indent, line); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
	}

	return m, nil
}

// appendPrincipal adds a "- principal" line to the open account.
func appendPrincipal(m PrincipalsMap, account, item string) error {
	principal, err := parseItem(item)
	if err != nil {
		return err
	}
	if account == "" {
		return fmt.Errorf("principal %q is not under any account", principal)
	}
	m[account] = append(m[account], principal)
	return nil
}

// addAccount records an "account:" line, returning the account that a
// following list item belongs to — "" when the line gave its principals
// inline and nothing may follow it.
func addAccount(m PrincipalsMap, indent int, line string) (string, error) {
	// An account is a key of the file's one top-level mapping, so it starts
	// at column 0. Anything indented is a nested structure this format does
	// not have, and reading it as another account would silently invent a
	// mapping the file never declared.
	if indent > 0 {
		return "", fmt.Errorf("%q is indented; an account starts at the beginning of the line", line)
	}

	name, principals, open, err := parseAccount(line)
	if err != nil {
		return "", err
	}
	if _, dup := m[name]; dup {
		return "", fmt.Errorf("account %q is already defined", name)
	}

	m[name] = principals
	if !open {
		return "", nil
	}
	return name, nil
}

// contentOf strips a line's comment and surrounding whitespace, returning
// how far it was indented and "" for a line with nothing on it. Indenting
// with a tab is rejected here rather than silently accepted: YAML forbids
// it outright, so a file using tabs was never being read the way its author
// expected.
func contentOf(raw string) (indent int, content string, err error) {
	line := stripComment(raw)
	indent = len(line) - len(strings.TrimLeft(line, " \t"))
	if strings.Contains(line[:indent], "\t") {
		return 0, "", fmt.Errorf("indented with a tab; YAML requires spaces")
	}
	return indent, strings.TrimSpace(line), nil
}

// parseItem reads what follows the "-" of a list item.
func parseItem(item string) (string, error) {
	// "-alice" is a plain scalar in YAML, not a list item. Requiring the
	// space keeps this parser from reading one as the other.
	if item != "" && !strings.HasPrefix(item, " ") {
		return "", fmt.Errorf("a list item needs a space after its %q", "-")
	}
	principal, err := unquote(strings.TrimSpace(item))
	if err != nil {
		return "", err
	}
	if principal == "" {
		return "", fmt.Errorf("empty principal")
	}
	return principal, nil
}

// parseAccount reads an "account:" line. open reports whether the account's
// list is still to come on following "- principal" lines, as opposed to
// having been given inline.
func parseAccount(line string) (name string, principals []string, open bool, err error) {
	key, value, ok := splitKey(line)
	if !ok {
		return "", nil, false, fmt.Errorf("expected an %q line or a %q list item, got %q", "account:", "- principal", line)
	}
	if name, err = unquote(key); err != nil {
		return "", nil, false, err
	}
	if name == "" {
		return "", nil, false, fmt.Errorf("empty account name")
	}

	switch {
	case value == "":
		// The list is on the lines below, or the account has none at all.
		return name, nil, true, nil
	case value == "null" || value == "~":
		return name, nil, false, nil
	case strings.HasPrefix(value, "["):
		principals, err = parseFlowSequence(value)
		return name, principals, false, err
	default:
		return "", nil, false, fmt.Errorf("account %q must be followed by a list of principals, got %q", name, value)
	}
}

// parseFlowSequence parses YAML's inline list form, "[]" or "[a, b]".
func parseFlowSequence(value string) ([]string, error) {
	inner, ok := strings.CutSuffix(value, "]")
	if !ok {
		return nil, fmt.Errorf("unterminated list %q", value)
	}
	inner = strings.TrimSpace(strings.TrimPrefix(inner, "["))
	if inner == "" {
		return []string{}, nil
	}

	// A naive split: a quoted principal containing a comma would be cut in
	// half here, which is why unquote rejects one rather than letting it
	// through mangled.
	principals := make([]string, 0, strings.Count(inner, ",")+1)
	for _, field := range strings.Split(inner, ",") {
		principal, err := unquote(strings.TrimSpace(field))
		if err != nil {
			return nil, err
		}
		if principal == "" {
			return nil, fmt.Errorf("empty principal in list %q", value)
		}
		principals = append(principals, principal)
	}
	return principals, nil
}

// splitKey splits an "account: value" line at the colon ending the key,
// ignoring one inside a quoted key. value is "" when the colon ends the
// line, which is the ordinary case.
func splitKey(line string) (key, value string, ok bool) {
	var inSingle, inDouble bool
	for i := range len(line) {
		switch c := line[i]; {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ':' && !inSingle && !inDouble:
			return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}

// stripComment removes a YAML end-of-line comment: a "#" that starts the
// line or follows whitespace, and is not inside a quoted scalar. A "#"
// anywhere else is an ordinary character of the value, which is why this
// scans rather than cutting at the first one.
func stripComment(line string) string {
	var inSingle, inDouble bool
	for i := range len(line) {
		switch c := line[i]; {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

// unquote strips a matching pair of surrounding quotes. Escapes are not
// interpreted: a quoted value carrying a backslash or another quote of the
// same kind is rejected, because reading it literally would silently
// authorize a principal spelled differently than the file says.
func unquote(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	quote := value[0]
	if quote != '"' && quote != '\'' {
		return value, nil
	}
	if len(value) < 2 || value[len(value)-1] != quote {
		return "", fmt.Errorf("unterminated quoted value %s", value)
	}

	inner := value[1 : len(value)-1]
	if strings.IndexByte(inner, quote) >= 0 || strings.Contains(inner, `\`) {
		return "", fmt.Errorf("quoted value %s uses escapes or embedded quotes, which are not supported", value)
	}
	return inner, nil
}

// Allowed reports whether any of certPrincipals is listed as allowed to
// assume account. An account with no entry in the map is never allowed,
// even if a certificate principal happens to match its name — callers that
// want an exact-match fallback do that themselves when no map applies.
func (m PrincipalsMap) Allowed(account string, certPrincipals []string) bool {
	allowed, ok := m[account]
	if !ok {
		return false
	}
	for _, p := range certPrincipals {
		if slices.Contains(allowed, p) {
			return true
		}
	}
	return false
}
