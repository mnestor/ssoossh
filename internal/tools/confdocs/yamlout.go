package confdocs

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// yamlWidth is where a generated comment line wraps. Narrow enough that the
// shipped /etc/ssoossh/ssoosshd.yaml reads comfortably in a terminal.
const yamlWidth = 74

// WriteDefaults renders server/config/defaults.yaml from the config structs:
// the comments from their doc comments, the values from their default: tags.
// Reports whether the file changed.
//
// The tag is the only place a default is written down. This file is what
// viper loads first and what ships to /etc/ssoossh/ssoosshd.yaml, so it has
// to hold real values -- but holding them is not the same as deciding them,
// and it used to do both. A value lived here and, for a handful of keys, in a
// constant beside the field as well, with nothing keeping the two in step.
// Now the file is output: `make confdocs` writes it, confdocs-check fails CI
// when it is stale, and server/config's golden test guards what the result
// loads to.
//
// A key with no default: tag is not written. Under its comment goes its
// example: tag, commented out, or nothing -- see writeYAMLField.
func WriteDefaults(path string, sections []*Section) (bool, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	var b strings.Builder
	b.WriteString(fileHeader())
	for _, s := range sections {
		writeSection(&b, s, CrossRefsIn(sections, s.Key))
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
func writeSection(b *strings.Builder, s *Section, refs map[string]string) {
	if s.Key == "" {
		// The scalars at the root of the file.
		for _, f := range s.Fields {
			writeYAMLField(b, f, refs, "", 0)
			b.WriteString("\n")
		}
		return
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
	if !sectionHasDefault(s) {
		b.WriteString("\n")
		return
	}

	fmt.Fprintf(b, "%s:\n", s.Key)
	for _, f := range s.Fields {
		writeYAMLField(b, f, refs, s.Key, 1)
	}
	b.WriteString("\n")
}

// sectionHasDefault reports whether any key in s has a default to write.
func sectionHasDefault(s *Section) bool {
	for _, f := range s.Fields {
		if hasDefault(f) {
			return true
		}
	}
	return false
}

// writeYAMLField emits one key: its comment, then its value if it has a
// default: tag, then its example: tag commented out if it has one instead.
func writeYAMLField(b *strings.Builder, f *Field, refs map[string]string, scope string, depth int) {
	// The rotation options belong to the embedded timberjack logger and have
	// no key of their own, so only their comment is emitted.
	if f.Embedded {
		writeComment(b, f.Doc, refs, scope, depth)
		return
	}

	indent := strings.Repeat("  ", depth)
	writeComment(b, f.Doc, refs, scope, depth)

	if f.IsStruct() {
		// A mapping key with nothing under it parses as null, which would add
		// a key the file never had. When no descendant has a default, the
		// comment documents the group and the key itself stays out.
		if !hasDefault(f) {
			b.WriteString("\n")
			return
		}
		fmt.Fprintf(b, "%s%s:\n", indent, f.Key)
		for _, c := range f.Children {
			writeYAMLField(b, c, refs, scope, depth+1)
		}
		return
	}

	if !f.HasDefault {
		// Documented but unset. The example: tag, when the field carries
		// one, names the key and shows a value that can be uncommented as
		// it stands. Without one, a blank line at least keeps the comment
		// from reading as the header for whichever key comes next.
		if f.Example != "" {
			fmt.Fprintf(b, "%s# %s: %s\n", indent, f.Key, f.Example)
			return
		}
		b.WriteString("\n")
		return
	}

	fmt.Fprintf(b, "%s%s: %s\n", indent, f.Key, renderDefault(f))
}

// hasDefault reports whether f, or anything below it, has a default to write.
func hasDefault(f *Field) bool {
	if f.HasDefault {
		return true
	}
	for _, c := range f.Children {
		if hasDefault(c) {
			return true
		}
	}
	return false
}

// renderDefault turns a default: tag into the YAML that follows the key.
//
// A string is quoted, because a bare scalar is retyped on the way back in:
// "true", "8080", "no" and "~" are all something other than strings to a YAML
// parser, and a key whose value happens to look like one of them would load
// as that instead. Every other type is written as the tag has it, which is
// how a list keeps its brackets, a duration its unit, and the two *bool keys
// their YAML null -- the tri-state those read as "not set, infer it".
func renderDefault(f *Field) string {
	if f.Type == "string" {
		return strconv.Quote(f.Default)
	}
	return f.Default
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
		"This file is generated. Both the comments and the values come from the",
		"config structs in server/config: the prose from each field's doc",
		"comment, the values from its `default:` tag. To change either, edit the",
		"struct field and run `make confdocs`; an edit made here is overwritten",
		"by the next run, and CI fails while the two disagree.",
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
