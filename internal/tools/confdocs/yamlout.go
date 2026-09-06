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
//
// A key whose default is the zero value for its type is written commented
// out as well. Loading the file with such a key set and loading it with the
// key absent hand the server the same value, so the comment costs nothing
// and says the one thing the bare key could not: this is an option to turn
// on, not a blank an operator is expected to fill in. What is left
// uncommented is then exactly the set of values ssoossh chose.
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
		writeFields(b, s.Fields, refs, "", 0, false)
		b.WriteString("\n")
		return
	}

	writeComment(b, s.Doc, refs, s.Key, 0)

	// A section every one of whose keys loads to the zero value ships no
	// opinion of its own, so its header is commented out along with them.
	// Left live over nothing but commented keys it would parse as null,
	// which is a key the file never had.
	//
	// The header used to be withheld outright in that case -- hsm, queue and
	// ldap.logging were prose and nothing else -- because a live header over
	// commented keys is exactly that null. Commenting the header out instead
	// keeps the whole block on the page, which is the point: an option an
	// operator cannot see the key for is an option they do not have.
	commented := !anyLive(s.Fields)
	fmt.Fprintf(b, "%s%s:\n", mark(commented), s.Key)
	writeFields(b, s.Fields, refs, s.Key, 1, commented)
	b.WriteString("\n")
}

// writeFields emits the keys of one block with a blank line between them.
// The separator is what makes a commented-out key findable: without it the
// key sits directly below the last line of its own paragraph and directly
// above the first line of the next one, and the whole block reads as prose
// with no settings in it.
func writeFields(b *strings.Builder, fields []*Field, refs map[string]string, scope string, depth int, commented bool) {
	for i, f := range fields {
		if i > 0 {
			b.WriteString("\n")
		}
		writeYAMLField(b, f, refs, scope, depth, commented)
	}
}

// mark is the prefix a commented-out key carries. It sits at the key's own
// indentation, so deleting the "# " leaves the key correctly indented
// whether or not the block above it is commented out too.
func mark(commented bool) string {
	if commented {
		return "# "
	}
	return ""
}

// writeYAMLField emits one key: its comment, then its value if it has a
// default: tag, then its example: tag commented out if it has one instead.
func writeYAMLField(b *strings.Builder, f *Field, refs map[string]string, scope string, depth int, commented bool) {
	indent := strings.Repeat("  ", depth)
	writeComment(b, f.Doc, refs, scope, depth)

	// The rotation options belong to the embedded timberjack logger and have
	// no key of their own, so the group's prose is followed by the keys
	// themselves, every one of them commented out: none is set until an
	// operator sets it. Without these the block was a paragraph naming keys
	// that appeared nowhere in the file it was describing.
	if f.Embedded {
		for _, k := range f.Keys {
			fmt.Fprintf(b, "%s# %s: %s\n", indent, k.Key, zeroValue(k.Type))
		}
		return
	}

	if f.IsStruct() {
		// A mapping key with nothing live under it parses as null, which
		// would add a key the file never had, so the key goes out commented
		// along with everything below it.
		sub := commented || !anyLive(f.Children)
		fmt.Fprintf(b, "%s%s%s:\n", indent, mark(sub), f.Key)
		writeFields(b, f.Children, refs, scope, depth+1, sub)
		return
	}

	if !f.HasDefault {
		// Documented but unset. A container of structs shows the shape of
		// one entry; anything else names the key and shows a value that can
		// be uncommented as it stands -- its example: tag when it has one,
		// the empty value for its type when it does not. The key goes out
		// either way: a paragraph with no key under it describes a setting
		// an operator then has to go and look up somewhere else.
		switch {
		case len(f.Elem) > 0:
			writeYAMLElem(b, f, depth)
		case f.Example != "":
			fmt.Fprintf(b, "%s# %s: %s\n", indent, f.Key, f.Example)
		case !docShowsKey(f.Doc, f.Key):
			fmt.Fprintf(b, "%s# %s: %s\n", indent, f.Key, zeroValue(f.Type))
		}
		return
	}

	fmt.Fprintf(b, "%s%s%s: %s\n", indent, mark(commented || isZeroDefault(f)), f.Key, renderDefault(f))
}

// docShowsKey reports whether a doc comment already writes the key out in a
// code block of its own. ssh_key's PEM block and fips's tri-state note both
// do, and a generated line under either would be a second, flatter answer to
// a question the prose has already answered better -- for fips a wrong one,
// since that key ships with no value precisely so that unset stays
// distinguishable from an explicit false.
func docShowsKey(doc []string, key string) bool {
	for _, line := range doc {
		t := strings.TrimSpace(line)
		t = strings.TrimSpace(strings.TrimPrefix(t, "#"))
		if strings.HasPrefix(t, key+":") {
			return true
		}
	}
	return false
}

// isZeroDefault reports whether f's default is the zero value for its type,
// which is what viper hands the server when the key is absent. Such a key is
// written commented out, so it reads as the option it is rather than as a
// blank someone forgot to fill in.
func isZeroDefault(f *Field) bool {
	if !f.HasDefault {
		return false
	}
	// An empty tag is the zero value for every type it is valid on: the
	// empty string, and the YAML null the *bool keys read as "infer it".
	if f.Default == "" {
		return true
	}
	// A zero duration is written both ways: the unit carries no information
	// once the number is nought, so "0" and "0s" are the same key unset.
	if f.Type == "duration" && f.Default == "0" {
		return true
	}
	return f.Default == zeroValue(f.Type)
}

// anyLive reports whether anything in fields, or below them, ships a value
// other than the zero value for its type.
func anyLive(fields []*Field) bool {
	for _, f := range fields {
		if f.HasDefault && !isZeroDefault(f) {
			return true
		}
		if anyLive(f.Children) {
			return true
		}
	}
	return false
}

// writeYAMLElem emits the shape of one entry under a container key: a
// commented-out block naming the key, one entry, and every key that entry
// can hold, at the indentation the real thing would sit at.
//
// A container of structs has no value to ship -- a map of directory fields
// or a list of policy tiers is deployment-specific, and inventing one would
// be writing policy here rather than documenting it -- so before this the
// file carried the group's prose and nothing whatever about what goes under
// it. The prose for each key stays in ssoosshd.yaml(5), which the file
// header already points at; what an operator cannot get from prose alone is
// the nesting, and that is what the block shows.
func writeYAMLElem(b *strings.Builder, f *Field, depth int) {
	indent := strings.Repeat("  ", depth)
	b.WriteString(indent + "#\n")
	b.WriteString(indent + "# One entry, and the keys it can hold:\n")
	b.WriteString(indent + "#\n")
	for _, line := range yamlEntry(f, "", "") {
		b.WriteString(indent + "#   " + line + "\n")
	}
}

// yamlEntry renders a container key and one entry under it. at indents the
// key's own line -- a list entry's first key carries a dash, so it does not
// always match pad -- and pad indents everything below it.
func yamlEntry(f *Field, at, pad string) []string {
	out := []string{at + f.Key + ":"}
	inner := pad + "  "

	// A map entry is named by the operator, so a placeholder stands in for
	// the key they choose. A list entry has only its position, so its first
	// key carries the dash instead.
	if f.Type == "map" {
		out = append(out, inner+"<name>:")
		return append(out, yamlEntryKeys(f.Elem, inner+"  ", false)...)
	}
	return append(out, yamlEntryKeys(f.Elem, inner+"  ", true)...)
}

// yamlEntryKeys renders the keys of one entry at pad. dash marks the first
// key of a list entry, which sits one level out with the dash in front of
// it so the rest of the entry lines up past it.
func yamlEntryKeys(fields []*Field, pad string, dash bool) []string {
	var out []string
	for _, f := range fields {
		at := pad
		if dash && len(out) == 0 {
			at = pad[:len(pad)-2] + "- "
		}
		switch {
		case f.IsStruct():
			out = append(out, at+f.Key+":")
			out = append(out, yamlEntryKeys(f.Children, pad+"  ", false)...)
		case len(f.Elem) > 0:
			out = append(out, yamlEntry(f, at, pad)...)
		default:
			out = append(out, at+f.Key+": "+yamlEntryValue(f))
		}
	}
	return out
}

// yamlEntryValue is the placeholder a skeleton key carries: its example: tag
// when it has one, otherwise the empty value for its type. The block is a
// shape to fill in, so an empty value is honest where an invented hostname
// would read as a recommendation.
func yamlEntryValue(f *Field) string {
	if f.Example != "" {
		return f.Example
	}
	return zeroValue(f.Type)
}

// zeroValue is the YAML for the zero value of a config-facing type: what a
// key holds when nobody sets it, and so both the placeholder a skeleton key
// carries and the value isZeroDefault measures a default against.
func zeroValue(typ string) string {
	switch typ {
	case "bool":
		return "false"
	case "int", "number":
		return "0"
	case "duration":
		return "0s"
	case "list":
		return "[]"
	case "map":
		return "{}"
	default:
		return `""`
	}
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
		"file installed as /etc/ssoossh/ssoosshd.yaml. Every commented-out key",
		"below is an option you can uncomment and set: it is either unset, or",
		"set to the zero value its own line shows, and commenting it out or",
		"leaving it out come to the same thing. The keys left uncommented are",
		"the values ssoossh actually chose. See ssoosshd.yaml(5) for the full",
		"reference.",
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
