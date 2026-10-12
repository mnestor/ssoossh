package configgolden

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// settingLine is a commented-out setting: the mark at the key's own
// indentation with no space after it, so deleting the one "#" is all it
// takes to turn it on. `grep -nE '^\s*#[a-z_]+:'` finds every one of them.
var settingLine = regexp.MustCompile(`^(\s*)#([a-z_][a-z0-9_]*):(\s.*)?$`)

// proseSetting is the old form, "# key:", which reads as one more line of
// the comment around it. A line of prose can take this shape too, so a
// match only counts when the key is one the configuration really has.
var proseSetting = regexp.MustCompile(`^\s*# ([a-z_][a-z0-9_]*):(\s|$)`)

// liveKey is an uncommented mapping key, the other kind of line a commented
// setting can sit beneath.
var liveKey = regexp.MustCompile(`^(\s*)([a-z_][a-z0-9_]*):(\s.*)?$`)

// childLine is a line of a commented-out block below its setting: the same
// mark at the same place, then the child's own indentation. Removing the
// "#" leaves the child nested exactly where it was written.
var childLine = regexp.MustCompile(`^ +\S`)

// AssertCommentedSettings checks every commented-out setting in doc, the
// text of a shipped defaults file. Each must be written "#key:", never
// "# key:", and deleting that single "#" -- from the setting, from the
// lines of its block, and from any commented-out keys it sits beneath --
// must leave YAML that parses, with the key at two spaces per level and at
// the path its indentation says it belongs to. cfg is the configuration
// struct the file loads into; its mapstructure keys tell a setting written
// the old way apart from a line of prose that happens to start "word:".
func AssertCommentedSettings(t *testing.T, doc string, cfg any) {
	t.Helper()

	for _, p := range commentedSettingProblems(doc, cfg) {
		t.Error(p)
	}
}

// commentedSettingProblems is AssertCommentedSettings without the test: one
// message per line that breaks the rule.
func commentedSettingProblems(doc string, cfg any) []string {
	lines := strings.Split(strings.TrimRight(doc, "\n"), "\n")

	keys := map[string]bool{}
	configKeys(reflect.TypeOf(cfg), keys, map[reflect.Type]bool{})

	var problems []string
	settings := 0
	for i, line := range lines {
		if m := proseSetting.FindStringSubmatch(line); m != nil && keys[m[1]] {
			problems = append(problems, fmt.Sprintf(
				"line %d: commented-out setting %q is written \"# %s:\"; write \"#%s:\" so it stands apart from the prose:\n%s",
				i+1, m[1], m[1], m[1], line))
		}
		if settingLine.MatchString(line) {
			settings++
			if err := checkUncomment(lines, i); err != nil {
				problems = append(problems, fmt.Sprintf(
					"line %d: uncommenting it does not give the setting it shows: %v\n%s", i+1, err, line))
			}
		}
	}
	if settings == 0 {
		problems = append(problems, `found no commented-out settings written "#key:"; a shipped defaults file is expected to have some`)
	}
	return problems
}

// configKeys collects every mapstructure key reachable from typ: the names
// a configuration file can actually use. A squashed field contributes its
// own fields and no name of its own.
func configKeys(typ reflect.Type, into map[string]bool, seen map[reflect.Type]bool) {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice ||
		typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return
	}
	seen[typ] = true

	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
		if name == "-" {
			continue
		}
		if !strings.Contains(","+opts+",", ",squash,") {
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			into[name] = true
		}
		configKeys(f.Type, into, seen)
	}
}

// checkUncomment turns on the setting at lines[at] the way an operator
// would -- the setting, the lines of its block, and the commented-out keys
// above it that it needs -- and reports what is wrong with the result.
func checkUncomment(lines []string, at int) error {
	m := settingLine.FindStringSubmatch(lines[at])
	indent := len(m[1])

	out := make([]string, len(lines))
	copy(out, lines)
	out[at] = uncomment(lines[at], indent)

	// The block's own lines, written under the setting with the mark in the
	// same column.
	children := 0
	mark := m[1] + "#"
	for j := at + 1; j < len(lines); j++ {
		rest, ok := strings.CutPrefix(lines[j], mark)
		if !ok || !childLine.MatchString(rest) {
			break
		}
		depth := len(rest) - len(strings.TrimLeft(rest, " "))
		if depth%2 != 0 {
			return fmt.Errorf("line %d is indented %d spaces under it once uncommented; nesting is two spaces a level", j+1, depth)
		}
		out[j] = uncomment(lines[j], indent)
		children++
	}

	// The keys it sits beneath, nearest first: each is the closest key line
	// above that is shallower than the one below it.
	var path []string
	path = append(path, m[2])
	below := indent
	for j := at - 1; j >= 0 && below > 0; j-- {
		key, ind, commented := keyLine(lines[j])
		if key == "" || ind >= below {
			continue
		}
		if commented {
			out[j] = uncomment(lines[j], ind)
		}
		path = append([]string{key}, path...)
		below = ind
	}
	if indent != 2*(len(path)-1) {
		return fmt.Errorf("the key is indented %d spaces at depth %d under %s; nesting is two spaces a level",
			indent, len(path)-1, strings.Join(path[:len(path)-1], "."))
	}

	text := strings.Join(out, "\n") + "\n"
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return fmt.Errorf("the file no longer parses: %w", err)
	}
	// Decoding into a map is what rejects a key that is now there twice.
	var asMap map[string]any
	if err := yaml.Unmarshal([]byte(text), &asMap); err != nil {
		return fmt.Errorf("the file no longer decodes: %w", err)
	}

	key, value := lookup(&root, path)
	if key == nil {
		return fmt.Errorf("%s is not in the parsed file", strings.Join(path, "."))
	}
	if key.Line != at+1 {
		return fmt.Errorf("%s parsed from line %d, not this one", strings.Join(path, "."), key.Line)
	}
	if children > 0 && value.Kind != yaml.MappingNode && value.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s has %d lines below it but parsed as a scalar", strings.Join(path, "."), children)
	}
	return nil
}

// uncomment deletes the "#" at column col of line.
func uncomment(line string, col int) string {
	return line[:col] + line[col+1:]
}

// keyLine reports the key a line names, its indentation, and whether it is
// commented out. Prose, blank lines and anything else give an empty key.
func keyLine(line string) (key string, indent int, commented bool) {
	if m := settingLine.FindStringSubmatch(line); m != nil {
		return m[2], len(m[1]), true
	}
	if m := liveKey.FindStringSubmatch(line); m != nil {
		return m[2], len(m[1]), false
	}
	return "", 0, false
}

// lookup walks path through the mappings below root and returns the key
// node and value node of its last element, or nils if it is not there.
func lookup(root *yaml.Node, path []string) (key, value *yaml.Node) {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, name := range path {
		if node.Kind != yaml.MappingNode {
			return nil, nil
		}
		key, value = nil, nil
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == name {
				key, value = node.Content[i], node.Content[i+1]
			}
		}
		if key == nil {
			return nil, nil
		}
		node = value
	}
	return key, value
}
