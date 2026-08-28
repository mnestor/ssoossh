// Package confdocs walks the ssoosshd configuration structs and returns the
// configuration surface as data: every key's dotted path, its type, and the
// prose from its Go doc comment.
//
// It is the shared front half of two generators. The Go structs in
// server/config are the single source of truth for what a key is called, what
// shape it takes, and what it means; docs/man/ssoosshd.yaml.5 and the comments
// in server/config/defaults.yaml are both rendered from this walk. Before it
// existed the same 127 keys were described by hand in all three places, and
// the man page had drifted to the point of omitting five whole sections.
//
// Values are deliberately not here. defaults.yaml stays the source of truth
// for what a default *is* -- it is the file viper loads and the file shipped
// to /etc/ssoossh -- and server/config's golden test guards it. This package
// supplies the prose that wraps those values.
package confdocs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Field is one configuration key, or one struct that contains more of them.
type Field struct {
	// Path is the dotted config path, e.g. "http.tls.min_version". Empty for
	// a squashed struct, whose children carry the parent's path.
	Path string

	// Key is the final path segment, e.g. "min_version".
	Key string

	// GoName is the Go field name, used to rewrite cross-references in prose.
	GoName string

	// Type is the config-facing type name: string, int, bool, duration, or
	// list. Empty for a struct.
	Type string

	// Doc is the field's Go doc comment, one entry per line, with the
	// leading "GoName " stripped from the first line.
	Doc []string

	// Example is the value from an `example:"..."` struct tag: the sample
	// WriteDefaults renders as a commented-out `key: value` line where a
	// field without a default: tag would otherwise be nothing but prose.
	// Rendered only in that case -- a field with a default shows its value,
	// and a second one above it would only disagree.
	//
	// A tag on a field none of whose group carries a default is inert: the
	// group is written as its own comment and nothing else, so there is no
	// key to hang the sample under. Those blocks put their sample in the
	// group's doc comment instead, the way hsm shows all of its keys at
	// once.
	Example string

	// Default is the value from a `default:"..."` struct tag: what
	// WriteDefaults writes after the key, and so what viper loads and what
	// ships in /etc/ssoossh/ssoosshd.yaml. The tag is the only statement of
	// the value; defaults.yaml is generated from it, never read back into
	// it.
	//
	// A field with no tag is not written at all -- its Example stands in, or
	// its doc comment stands alone.
	Default string

	// HasDefault separates "no default: tag" from `default:""`, which is a
	// real value: the empty string for a string key, and YAML null for the
	// pointer keys that read unset as "infer it".
	HasDefault bool

	// Children is non-empty when this field is a struct.
	Children []*Field

	// Embedded marks a field promoted from an embedded third-party struct
	// (the timberjack rotation options). Its keys are not walked
	// individually: the surface belongs to another module, so it is
	// documented as one group by the doc comment on the embedded field.
	Embedded bool
}

// IsStruct reports whether f groups other keys rather than holding a value.
func (f *Field) IsStruct() bool { return len(f.Children) > 0 }

// Section is one top-level grouping in the generated output: a struct
// directly under Config, or the synthetic group holding Config's own scalars.
type Section struct {
	// Key is the top-level config key, e.g. "http". Empty for the scalars
	// that sit at the root of the file.
	Key string

	// Title is the section heading.
	Title string

	// Doc is the struct's own doc comment.
	Doc []string

	// Fields are the keys in this section, in declaration order.
	Fields []*Field
}

// pkg is one parsed Go package: its struct types by name.
type pkg struct {
	structs map[string]*ast.StructType
	docs    map[string]*ast.CommentGroup
	// aliases maps a named non-struct type to its underlying type
	// expression, so `type DBProvider string` renders as a string.
	aliases map[string]ast.Expr
}

// Walk parses the given package directories and returns the configuration
// surface rooted at the named struct. dirs[0] must contain root.
func Walk(dirs []string, root string) ([]*Section, error) {
	packages := map[string]*pkg{}

	for _, dir := range dirs {
		parsed, err := parseDir(dir)
		if err != nil {
			return nil, err
		}
		for name, files := range parsed {
			collect(packages, name, files)
		}
	}

	base := filepath.Base(dirs[0])
	rootPkg, ok := packages[base]
	if !ok {
		return nil, fmt.Errorf("package %q not found in %s", base, dirs[0])
	}
	st, ok := rootPkg.structs[root]
	if !ok {
		return nil, fmt.Errorf("struct %q not found in package %q", root, base)
	}

	return sections(packages, base, st)
}

// parseDir parses the non-test Go files in dir, grouped by package name.
//
// go/parser.ParseDir did this in one call but is deprecated, and the
// replacement it points at (golang.org/x/tools/go/packages) type-checks the
// module to answer a question this generator never asks: it reads
// declarations and doc comments, never types. Reading the directory keeps
// that cost, and a direct dependency on x/tools, out of the build. Files
// arrive in name order rather than the map order ParseDir returned, so a
// package split across several files now walks the same way every run.
func parseDir(dir string) (map[string][]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	out := map[string][]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		out[file.Name.Name] = append(out[file.Name.Name], file)
	}
	return out, nil
}

// collect records every struct type and named type in a parsed package.
func collect(packages map[string]*pkg, name string, files []*ast.File) {
	target, ok := packages[name]
	if !ok {
		target = &pkg{
			structs: map[string]*ast.StructType{},
			docs:    map[string]*ast.CommentGroup{},
			aliases: map[string]ast.Expr{},
		}
		packages[name] = target
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			target.record(gd)
		}
	}
}

// record stores the types declared by one `type` declaration, which may be a
// parenthesised group of several.
func (p *pkg) record(gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		// A single-spec declaration carries its doc on the GenDecl.
		doc := ts.Doc
		if doc == nil {
			doc = gd.Doc
		}
		p.docs[ts.Name.Name] = doc

		if st, ok := ts.Type.(*ast.StructType); ok {
			p.structs[ts.Name.Name] = st
			continue
		}
		p.aliases[ts.Name.Name] = ts.Type
	}
}

// sections splits the root struct into top-level groups: every struct-valued
// field becomes its own section, and the remaining scalars are gathered into
// a leading section with no key of its own.
func sections(packages map[string]*pkg, base string, root *ast.StructType) ([]*Section, error) {
	var top []*Section
	var scalars []*Field

	for _, f := range root.Fields.List {
		field, err := build(packages, base, f, "")
		if err != nil {
			return nil, err
		}
		if field == nil {
			continue
		}

		// A squashed struct's children are top-level keys.
		if field.Path == "" && field.IsStruct() {
			for _, c := range field.Children {
				if c.IsStruct() {
					top = append(top, &Section{Key: c.Key, Title: c.Key, Doc: c.Doc, Fields: c.Children})
					continue
				}
				scalars = append(scalars, c)
			}
			continue
		}

		if field.IsStruct() {
			top = append(top, &Section{Key: field.Key, Title: field.Key, Doc: field.Doc, Fields: field.Children})
			continue
		}
		scalars = append(scalars, field)
	}

	out := []*Section{{Key: "", Title: "Top level", Fields: scalars}}
	out = append(out, top...)
	return out, nil
}

// build converts one AST field into a Field, recursing into struct types.
// Returns nil for a field mapstructure ignores.
func build(packages map[string]*pkg, base string, f *ast.Field, prefix string) (*Field, error) {
	if len(f.Names) == 0 {
		return buildEmbedded(f), nil
	}
	name := f.Names[0].Name
	if !ast.IsExported(name) {
		return nil, nil
	}

	key, squash, ok := mapstructureKey(f.Tag)
	if !ok {
		return nil, nil
	}

	path := fieldPath(prefix, key, squash)

	out := &Field{
		Path:    path,
		Key:     key,
		GoName:  name,
		Doc:     docLines(f.Doc, name),
		Example: tagValue(f.Tag, "example"),
	}

	// Resolve the type: a struct becomes children, anything else a scalar.
	pkgName, typeName, list := typeName(f.Type)
	if pkgName == "" {
		pkgName = base
	}
	if p, ok := packages[pkgName]; ok {
		if st, ok := p.structs[typeName]; ok && !list {
			childPrefix := path
			if squash {
				childPrefix = prefix
			}
			children, err := buildChildren(packages, pkgName, st, childPrefix)
			if err != nil {
				return nil, err
			}
			out.Children = children
			applyChildDefaults(f.Tag, children)
			// The field's comment and the type's comment are both prose
			// about this key, and each routinely carries something the
			// other does not -- the LDAP field says what it is for, the
			// type says the server does not consume it yet. Join them, and
			// drop the "See LDAPConfig." pointer that only made sense while
			// the two were apart.
			out.Doc = joinDocs(out.Doc, docLines(p.docs[typeName], typeName), typeName)
			return out, nil
		}
		// A named scalar type (type DBProvider string) renders as its base.
		if underlying, ok := p.aliases[typeName]; ok {
			_, typeName, list = typeName2(underlying)
		}
	}

	rendered, err := renderType(typeName, list)
	if err != nil {
		return nil, fmt.Errorf("%s (%s): %w", path, name, err)
	}
	out.Type = rendered
	out.Default, out.HasDefault = tagLookup(f.Tag, "default")
	return out, nil
}

// applyChildDefaults lets the field instantiating a struct set the defaults
// for that instantiation, through `default_<key>` tags written beside the
// mapstructure tag. One type behind several keys is the case it exists for:
// GenericLogging is db.logging, queue.logging, ldap.logging and mail.logging
// at once, and those four ship different values, so the value cannot live on
// the type the way it does for a struct with a single home.
//
// An override always wins: the instantiation is the more specific statement.
func applyChildDefaults(tag *ast.BasicLit, children []*Field) {
	for _, c := range children {
		if v, ok := tagLookup(tag, "default_"+c.Key); ok {
			c.Default, c.HasDefault = v, true
		}
	}
}

// buildEmbedded documents an embedded field as a single group. Such a field
// carries no name of its own: its keys land in the enclosing struct's
// namespace but belong to another module, so it is documented as one group,
// and only when we have written a doc comment saying what that group is.
// Returns nil when we have not.
func buildEmbedded(f *ast.Field) *Field {
	_, name, _ := typeName(f.Type)
	doc := docLines(f.Doc, name)
	if len(doc) == 0 {
		return nil
	}
	return &Field{
		Key:      name,
		GoName:   name,
		Doc:      doc,
		Embedded: true,
	}
}

// fieldPath is the dotted config path for key under prefix. A squashed struct
// has no path of its own -- its children carry the parent's.
func fieldPath(prefix, key string, squash bool) string {
	if squash {
		return ""
	}
	if prefix == "" || key == "" {
		return key
	}
	return prefix + "." + key
}

// buildChildren converts the fields of a struct type, skipping the ones
// mapstructure ignores.
func buildChildren(packages map[string]*pkg, pkgName string, st *ast.StructType, prefix string) ([]*Field, error) {
	var out []*Field
	for _, cf := range st.Fields.List {
		child, err := build(packages, pkgName, cf, prefix)
		if err != nil {
			return nil, err
		}
		if child == nil {
			continue
		}
		out = append(out, child)
	}
	return out, nil
}

// typeName2 is typeName for an already-resolved expression.
func typeName2(e ast.Expr) (string, string, bool) { return typeName(e) }

// typeName reduces a type expression to (package, name, isList), unwrapping
// pointers and slices.
func typeName(e ast.Expr) (string, string, bool) {
	list := false
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ArrayType:
			list = true
			e = t.Elt
		case *ast.MapType:
			return "", "map", false
		case *ast.SelectorExpr:
			if id, ok := t.X.(*ast.Ident); ok {
				return id.Name, t.Sel.Name, list
			}
			return "", t.Sel.Name, list
		case *ast.Ident:
			return "", t.Name, list
		default:
			return "", "", list
		}
	}
}

// renderType maps a Go type onto the name used in the documentation. An
// unrecognised type is an error rather than a guess: silently rendering it as
// "string" is how a generated page starts lying about the config surface.
func renderType(name string, list bool) (string, error) {
	if list {
		return "list", nil
	}
	switch name {
	case "Duration":
		return "duration", nil
	case "string":
		return "string", nil
	case "bool":
		return "bool", nil
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return "int", nil
	case "float32", "float64":
		return "number", nil
	case "map":
		return "map", nil
	default:
		return "", fmt.Errorf("unhandled config field type %q", name)
	}
}

// mapstructureKey returns the config key from a field's tag, whether the
// field is squashed into its parent, and whether it is mapped at all.
func mapstructureKey(tag *ast.BasicLit) (string, bool, bool) {
	raw := tagValue(tag, "mapstructure")
	if raw == "" {
		return "", false, false
	}
	parts := strings.Split(raw, ",")
	key := parts[0]
	squash := false
	for _, opt := range parts[1:] {
		if opt == "squash" {
			squash = true
		}
	}
	if key == "-" {
		return "", false, false
	}
	if key == "" && !squash {
		return "", false, false
	}
	return key, squash, true
}

// tagValue reads one key out of a struct tag literal.
func tagValue(tag *ast.BasicLit, key string) string {
	v, _ := tagLookup(tag, key)
	return v
}

// tagLookup reads one key out of a struct tag literal, reporting whether it
// was there at all. default: needs the distinction that tagValue cannot make:
// an absent tag means the key is not written, while `default:""` is a value.
func tagLookup(tag *ast.BasicLit, key string) (string, bool) {
	if tag == nil {
		return "", false
	}
	return reflect.StructTag(strings.Trim(tag.Value, "`")).Lookup(key)
}

// docLines cleans a doc comment into plain lines: comment markers removed,
// the conventional leading "FieldName " dropped so the prose reads as a
// description of the key rather than of the Go field, and any trailing blank
// lines trimmed.
func docLines(doc *ast.CommentGroup, name string) []string {
	if doc == nil {
		return nil
	}

	var lines []string
	for _, c := range doc.List {
		text := c.Text
		switch {
		case strings.HasPrefix(text, "//"):
			text = strings.TrimPrefix(text, "//")
		case strings.HasPrefix(text, "/*"):
			text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
		}
		for _, line := range strings.Split(text, "\n") {
			lines = append(lines, strings.TrimRight(strings.TrimPrefix(line, " "), " \t"))
		}
	}

	// Drop a "not covered:" or directive line; they are for Go readers.
	var kept []string
	for _, l := range lines {
		if strings.HasPrefix(l, "go:") || strings.HasPrefix(l, "nolint:") {
			continue
		}
		kept = append(kept, l)
	}
	lines = kept

	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}

	// "Port is the TCP port ..." -> "The TCP port ..."; leave anything that
	// does not follow the convention alone. Both copulas are handled: a
	// plural field reads "Extensions are the ...", and dropping only the
	// name would leave the sentence starting "Are the ...".
	if rest, ok := trimCopula(lines[0], name); ok {
		lines[0] = strings.ToUpper(rest[:1]) + rest[1:]
	} else if strings.HasPrefix(lines[0], name+", ") {
		lines[0] = strings.TrimPrefix(lines[0], name+", ")
	} else if strings.HasPrefix(lines[0], name+" ") {
		lines[0] = strings.TrimPrefix(lines[0], name+" ")
		if lines[0] != "" {
			lines[0] = strings.ToUpper(lines[0][:1]) + lines[0][1:]
		}
	}

	return lines
}

// seeRef matches a bare "See TypeName." cross-reference.
var seeRef = regexp.MustCompile(`\s*See ` + "`?" + `([A-Z][A-Za-z0-9]*)` + "`?" + `\.`)

// joinDocs combines a field's doc comment with its type's.
//
// Both describe the same key and their opening paragraphs routinely say the
// same thing twice ("Logging configures the main application log" /
// "AppLogging configures the application's main log output"). So when the
// field introduces the key, the type's opening paragraph is dropped and only
// its later paragraphs are kept -- which is where the detail that only the
// type knows lives, such as LDAP not being consumed yet.
func joinDocs(field, typeDoc []string, typeName string) []string {
	strip := func(lines []string) []string {
		var out []string
		for _, l := range lines {
			l = seeRef.ReplaceAllStringFunc(l, func(m string) string {
				if strings.Contains(m, typeName) {
					return ""
				}
				return m
			})
			out = append(out, strings.TrimRight(l, " "))
		}
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		return out
	}

	field = strip(field)
	typeDoc = strip(typeDoc)
	switch {
	case len(field) == 0:
		return typeDoc
	case len(typeDoc) == 0:
		return field
	}

	rest := paragraphsAfterFirst(typeDoc)
	if len(rest) == 0 {
		return field
	}
	return append(append(append([]string{}, field...), ""), rest...)
}

// paragraphsAfterFirst drops everything up to and including the first blank
// line, leaving the paragraphs that follow.
func paragraphsAfterFirst(lines []string) []string {
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			rest := lines[i+1:]
			for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
				rest = rest[1:]
			}
			return rest
		}
	}
	return nil
}

// trimCopula removes a leading "Name is " or "Name are " from a doc comment's
// opening sentence, reporting whether it found one.
func trimCopula(line, name string) (string, bool) {
	for _, copula := range []string{" is ", " are "} {
		if prefix := name + copula; strings.HasPrefix(line, prefix) {
			rest := strings.TrimPrefix(line, prefix)
			if rest != "" {
				return rest, true
			}
		}
	}
	return "", false
}

// CrossRefs builds the Go-field-name to dotted-path map used to rewrite
// references like "see TrustedProxies" into the key a reader of the config
// file would recognise.
//
// Names that resolve to more than one path are dropped here: RequireGroup is
// four different keys, and an ambiguous rewrite is worse than none. Ambiguity
// is resolved per-section instead, by CrossRefsIn.
func CrossRefs(sections []*Section) map[string]string {
	seen := map[string][]string{}
	var walk func(fields []*Field)
	walk = func(fields []*Field) {
		for _, f := range fields {
			if f.Path != "" {
				seen[f.GoName] = append(seen[f.GoName], f.Path)
			}
			walk(f.Children)
		}
	}
	for _, s := range sections {
		walk(s.Fields)
	}

	out := map[string]string{}
	for name, paths := range seen {
		if unique := dedupe(paths); len(unique) == 1 {
			out[name] = unique[0]
		}
	}
	return out
}

// CrossRefsIn is CrossRefs with one section's own keys taking precedence, so
// a name that is ambiguous across the file still resolves inside the section
// that defines it: RequireGroup under admin means admin.require_group.
func CrossRefsIn(sections []*Section, scope string) map[string]string {
	out := map[string]string{}
	for name, path := range CrossRefs(sections) {
		out[name] = path
	}
	if scope == "" {
		return out
	}

	for _, s := range sections {
		if s.Key != scope {
			continue
		}
		local := map[string][]string{}
		var walk func(fields []*Field)
		walk = func(fields []*Field) {
			for _, f := range fields {
				if f.Path != "" {
					local[f.GoName] = append(local[f.GoName], f.Path)
				}
				walk(f.Children)
			}
		}
		walk(s.Fields)

		for name, paths := range local {
			if unique := dedupe(paths); len(unique) == 1 {
				out[name] = unique[0]
			}
		}
	}
	return out
}

// dedupe sorts and removes duplicates.
func dedupe(paths []string) []string {
	sort.Strings(paths)
	unique := paths[:0]
	for i, p := range paths {
		if i == 0 || p != paths[i-1] {
			unique = append(unique, p)
		}
	}
	return unique
}
