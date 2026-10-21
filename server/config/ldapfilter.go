package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	// LDAP filters are a plain-text grammar, not HTML. The template is only
	// parsed here, never executed; the service renders it with RFC 4515
	// escaping (server/service/ldapclient.go).
	"text/template" // nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"text/template/parse"
)

// ldapIdentityFields are the filter-data fields that differ between two
// logins: the OIDC username, email, subject and extra claims. The primary
// lookup also renders against DN and Attr, but both are empty there, so a
// user_filter that reads only them still names nobody in particular.
var ldapIdentityFields = []string{"Username", "Email", "Subject", "Extra"}

// errUserFilterNoIdentity is the startup error for a user_filter that
// renders the same for everyone.
const errUserFilterNoIdentity = "ldap.user_filter must reference the identity ({{.Username}}, {{.Email}}, {{.Subject}} or {{.Extra.<name>}}), or every login resolves to the same entry"

// validateLDAPUserFilter refuses a user_filter that does not parse or that
// references no identity field. A filter with no identity field renders to
// the same string for every login: either it matches several entries and
// every lookup fails, or it matches one and links everyone to it, handing
// that person's accounts and groups to every login.
func validateLDAPUserFilter(text string) error {
	ok, err := templateReferencesFields(text, ldapIdentityFields)
	if err != nil {
		return fmt.Errorf("ldap.user_filter is not a valid template: %w", err)
	}
	if !ok {
		return errors.New(errUserFilterNoIdentity)
	}
	return nil
}

// templateReferencesFields reports whether the template reads any of the
// named top-level fields, as .Name, .Name.key, $.Name or index .Name "key".
// It walks the parse tree rather than matching text, so a name inside a
// template comment or a string literal does not count, and one feeding a
// pipeline or a function argument does. Templates reached through
// {{template "name"}} are walked too; one defined and never invoked is not.
func templateReferencesFields(text string, fields []string) (bool, error) {
	// The service injects ldapEscape around every action and may be given
	// it by hand, so the parse accepts the name. Any func(string) string
	// will do: the template is never executed here.
	tmpl, err := template.New("filter").
		Funcs(template.FuncMap{"ldapEscape": strings.Clone}).
		Parse(text)
	if err != nil {
		return false, err
	}
	if tmpl.Tree == nil {
		// not covered: unreachable. A successful Parse always sets the
		// root template's tree, empty text included; the guard keeps the
		// walk below from dereferencing nil if that ever changes.
		return false, nil
	}
	w := &fieldWalker{tmpl: tmpl, fields: fields, visited: map[string]bool{tmpl.Name(): true}}
	return w.walk(tmpl.Root), nil
}

// fieldWalker carries the state of one parse-tree walk: the template set to
// resolve {{template}} calls in, the names being looked for, and the
// templates already entered so a recursive definition terminates.
type fieldWalker struct {
	tmpl    *template.Template
	fields  []string
	visited map[string]bool
}

// walk reports whether node, or anything beneath it, reads one of the
// fields. Node kinds that carry no expression (text, comments, break and
// continue, literals) cannot read a field and fall through to false.
func (w *fieldWalker) walk(node parse.Node) bool {
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return false
		}
		return slices.ContainsFunc(n.Nodes, w.walk)
	case *parse.ActionNode:
		return w.walkPipe(n.Pipe)
	case *parse.IfNode:
		return w.walkBranch(&n.BranchNode)
	case *parse.RangeNode:
		return w.walkBranch(&n.BranchNode)
	case *parse.WithNode:
		return w.walkBranch(&n.BranchNode)
	case *parse.TemplateNode:
		return w.walkPipe(n.Pipe) || w.walkTemplate(n.Name)
	case *parse.PipeNode:
		return w.walkPipe(n)
	case *parse.CommandNode:
		return slices.ContainsFunc(n.Args, w.walk)
	case *parse.ChainNode:
		return w.walk(n.Node)
	case *parse.FieldNode:
		return len(n.Ident) > 0 && slices.Contains(w.fields, n.Ident[0])
	case *parse.VariableNode:
		// $.Username reads the root data; $x.Username reads whatever $x
		// was bound to, which the pipeline that bound it already counted.
		return len(n.Ident) > 1 && n.Ident[0] == "$" && slices.Contains(w.fields, n.Ident[1])
	default:
		return false
	}
}

// walkPipe walks a pipeline's commands. Variable declarations need no walk
// of their own: the value bound is one of the commands.
func (w *fieldWalker) walkPipe(pipe *parse.PipeNode) bool {
	if pipe == nil {
		return false
	}
	return slices.ContainsFunc(pipe.Cmds, func(c *parse.CommandNode) bool { return w.walk(c) })
}

// walkBranch walks an if, range or with: its condition and both branches.
func (w *fieldWalker) walkBranch(b *parse.BranchNode) bool {
	return w.walkPipe(b.Pipe) || w.walk(b.List) || w.walk(b.ElseList)
}

// walkTemplate enters a {{template "name"}} call's definition once.
func (w *fieldWalker) walkTemplate(name string) bool {
	if w.visited[name] {
		return false
	}
	w.visited[name] = true
	called := w.tmpl.Lookup(name)
	if called == nil || called.Tree == nil {
		// Calling an undefined template fails at execution, not at parse.
		return false
	}
	return w.walk(called.Root)
}
