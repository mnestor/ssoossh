package confdocs

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// yamlWidth is where a generated comment line wraps. Narrow enough that the
// shipped /etc/ssoossh/ssoosshd.yaml reads comfortably in a terminal.
const yamlWidth = 74

// WriteDefaults renders server/config/defaults.yaml: the comments from the
// config structs, and every value taken unchanged from the file it replaces.
// Reports whether the file changed.
//
// Values are read, never invented. This file is what viper loads and what
// ships to /etc/ssoossh, and server/config's golden test guards its values;
// generating them from a second source would put two things in charge of one
// number. The structs supply only the prose.
//
// A key the file does not set still gets its comment. Under it goes the
// field's example: tag, commented out, so the prose names the key it
// describes and a reader can uncomment a working line; a field with no tag
// gets a blank line instead. That is how ssh_key, hsm, and fips appear:
// documented in place, shown by example, and left unset.
//
// An example is not a default. It is prose from the struct, like the doc
// comment beside it, and it is rendered only where the file sets nothing --
// never above a live value, where it would be a second answer to a question
// the value already answers.
func WriteDefaults(path string, sections []*Section) (bool, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(before, &doc); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}

	values := map[string]*yaml.Node{}
	if len(doc.Content) > 0 {
		indexValues(doc.Content[0], "", values)
	}

	var b strings.Builder
	b.WriteString(fileHeader())

	for _, s := range sections {
		if err := writeSection(&b, s, values, CrossRefsIn(sections, s.Key)); err != nil {
			return false, err
		}
	}

	next := []byte(strings.TrimRight(b.String(), "\n") + "\n")
	if bytes.Equal(next, before) {
		return false, nil
	}
	if err := os.WriteFile(path, next, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// writeSection emits one section: its comment, its key, and its fields.
func writeSection(b *strings.Builder, s *Section, values map[string]*yaml.Node, refs map[string]string) error {
	if s.Key == "" {
		// The scalars at the root of the file.
		for _, f := range s.Fields {
			if err := writeYAMLField(b, f, values, refs, "", 0); err != nil {
				return err
			}
			b.WriteString("\n")
		}
		return nil
	}

	writeComment(b, s.Doc, refs, s.Key, 0)

	// Same reason as a struct field: a section header with nothing set
	// beneath it would parse as a null key the file never had. hsm and
	// queue are documented this way -- comment emitted, key withheld -- and
	// left unset.
	//
	// The fields go with it. Written anyway they would be indented under a
	// header that is not there, which reads as a run of loose sentences with
	// no key attached to any of them; the section comment carries the shape
	// of the block and ssoosshd.yaml(5) carries the per-key detail.
	if !sectionHasValue(s, values) {
		b.WriteString("\n")
		return nil
	}

	fmt.Fprintf(b, "%s:\n", s.Key)
	for _, f := range s.Fields {
		if err := writeYAMLField(b, f, values, refs, s.Key, 1); err != nil {
			return err
		}
	}
	b.WriteString("\n")
	return nil
}

// sectionHasValue reports whether the file being replaced set any key in s.
func sectionHasValue(s *Section, values map[string]*yaml.Node) bool {
	for _, f := range s.Fields {
		if hasValue(f, values) {
			return true
		}
	}
	return false
}

// writeYAMLField emits one key: its comment, then its value if the file being
// replaced had one.
func writeYAMLField(b *strings.Builder, f *Field, values map[string]*yaml.Node, refs map[string]string, scope string, depth int) error {
	// The rotation options belong to the embedded timberjack logger and have
	// no key of their own, so only their comment is emitted.
	if f.Embedded {
		writeComment(b, f.Doc, refs, scope, depth)
		return nil
	}

	indent := strings.Repeat("  ", depth)

	if f.IsStruct() {
		writeComment(b, f.Doc, refs, scope, depth)
		// A mapping key with nothing under it parses as null, which would add
		// a key the file never had. When no descendant is set, the comment
		// documents the group and the key itself stays out.
		if !hasValue(f, values) {
			b.WriteString("\n")
			return nil
		}
		fmt.Fprintf(b, "%s%s:\n", indent, f.Key)
		for _, c := range f.Children {
			if err := writeYAMLField(b, c, values, refs, scope, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	writeComment(b, f.Doc, refs, scope, depth)

	node, ok := values[f.Path]
	if !ok {
		// Documented but unset. The example: tag, when the field carries
		// one, names the key and shows a value that can be uncommented as
		// it stands. Without one, a blank line at least keeps the comment
		// from reading as the header for whichever key comes next.
		if f.Example != "" {
			fmt.Fprintf(b, "%s# %s: %s\n", indent, f.Key, f.Example)
			return nil
		}
		b.WriteString("\n")
		return nil
	}

	rendered, err := renderValue(node, depth)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	fmt.Fprintf(b, "%s%s:%s\n", indent, f.Key, rendered)
	return nil
}

// hasValue reports whether f, or anything below it, is set in the file.
func hasValue(f *Field, values map[string]*yaml.Node) bool {
	if f.Path != "" {
		if _, ok := values[f.Path]; ok {
			return true
		}
	}
	for _, c := range f.Children {
		if hasValue(c, values) {
			return true
		}
	}
	return false
}

// renderValue encodes one value node, preserving the quoting style it had.
func renderValue(node *yaml.Node, depth int) (string, error) {
	// An explicitly empty value ("cookie_secure:") is null, not the empty
	// string, and re-quoting it would change what viper unmarshals.
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return "", nil
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}

	text := strings.TrimRight(buf.String(), "\n")
	if text == "" {
		return ` ""`, nil
	}

	// A scalar encodes to one line and sits after the colon; a collection
	// encodes to several and is indented beneath it.
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		return " " + lines[0], nil
	}

	indent := strings.Repeat("  ", depth+1)
	var b strings.Builder
	b.WriteString("\n")
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(indent + l + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// indexValues records every value node in the file by dotted path.
func indexValues(node *yaml.Node, prefix string, out map[string]*yaml.Node) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		path := key.Value
		if prefix != "" {
			path = prefix + "." + key.Value
		}
		if value.Kind == yaml.MappingNode {
			indexValues(value, path, out)
			continue
		}
		out[path] = value
	}
}

// writeComment renders a doc comment as YAML comment lines at the given depth.
func writeComment(b *strings.Builder, doc []string, refs map[string]string, scope string, depth int) {
	if len(doc) == 0 {
		return
	}
	indent := strings.Repeat("  ", depth)
	for _, line := range wrapComment(doc, refs, scope) {
		if line == "" {
			b.WriteString(indent + "#\n")
			continue
		}
		b.WriteString(indent + "# " + line + "\n")
	}
}

// fileHeader is the banner at the top of the generated file.
func fileHeader() string {
	lines := []string{
		"ssoosshd configuration.",
		"",
		"The comments in this file are generated from the doc comments on the",
		"config structs in server/config. To change a description, edit the",
		"struct field and run `make confdocs`. The values are edited here.",
		"",
		"This is both the defaults embedded in the binary and the annotated",
		"file installed as /etc/ssoossh/ssoosshd.yaml. A key documented with",
		"no value below is deliberately unset; see ssoosshd.yaml(5) for the",
		"full reference.",
	}
	var b strings.Builder
	for _, l := range lines {
		if l == "" {
			b.WriteString("#\n")
			continue
		}
		b.WriteString("# " + l + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// wrapComment reflows doc lines to the file's width. An already-indented line
// is left alone: doc comments use indentation for examples a reader can copy,
// and reflowing those would break them.
func wrapComment(doc []string, refs map[string]string, scope string) []string {
	var out []string
	var para []string

	flush := func() {
		if len(para) == 0 {
			return
		}
		out = append(out, wrapWords(strings.Join(para, " "))...)
		para = nil
	}

	for _, line := range StripGoOnlyRefs(doc, refs) {
		line = rewriteRefs(line, refs, scope)
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			out = append(out, "")
		case strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"):
			// Go doc marks a code block with a leading tab. In a config file
			// the reader wants to uncomment the line, so render it as spaces.
			flush()
			out = append(out, strings.TrimRight(expandTabs(line), " "))
		default:
			para = append(para, strings.TrimSpace(line))
		}
	}
	flush()

	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// expandTabs turns the leading tabs of a doc-comment code block into the two
// spaces the surrounding YAML uses.
func expandTabs(line string) string {
	var n int
	for n < len(line) && line[n] == '\t' {
		n++
	}
	return strings.Repeat("  ", n) + line[n:]
}

// wrapWords greedily wraps a paragraph to yamlWidth.
func wrapWords(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var out []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > yamlWidth {
			out = append(out, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(out, line)
}
