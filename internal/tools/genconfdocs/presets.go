package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mnestor/ssoossh/internal/tools/confdocs"
	"github.com/mnestor/ssoossh/server/config"
)

// The ldap.schema presets page is hand-written prose around one generated
// table, bounded by these markers. The table is built from the embedded
// preset files themselves, so it cannot describe values the server does
// not merge.
const (
	presetsPage  = "user-docs/src/content/docs/operations/ldap-schema-presets.md"
	presetsBegin = "<!-- BEGIN GENERATED: ldap.schema presets (make confdocs) -->"
	presetsEnd   = "<!-- END GENERATED: ldap.schema presets -->"

	// presetNote is what every key a preset sets must say in its doc
	// comment, so a reader of that one key learns the preset exists.
	presetNote = "ldap.schema"
)

// presetTable renders one row per key any preset sets, in the order the
// config structs declare them, with a column for the built-in default and
// one per preset.
func presetTable(sections []*confdocs.Section) (string, error) {
	order, _ := fieldIndex(sections)

	values := map[string]map[string]string{}
	for _, schema := range config.LDAPSchemas() {
		settings, err := config.LDAPSchemaPresetSettings(schema)
		if err != nil {
			return "", err
		}
		for _, s := range settings {
			if _, ok := order[s.Key]; !ok {
				return "", fmt.Errorf("the %s preset sets %s, which no config struct declares", schema, s.Key)
			}
			if values[s.Key] == nil {
				values[s.Key] = map[string]string{}
			}
			values[s.Key][schema] = s.Value
		}
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int { return order[a] - order[b] })

	var b strings.Builder
	b.WriteString("| Key | Unset (built-in default) |")
	for _, schema := range config.LDAPSchemas() {
		fmt.Fprintf(&b, " `%s` |", schema)
	}
	b.WriteString("\n| --- | --- |")
	for range config.LDAPSchemas() {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| [`%s`](%s) | %s |", k, referenceURL(k), presetCell(config.DefaultSettingValue(k), "empty"))
		for _, schema := range config.LDAPSchemas() {
			value, set := values[k][schema]
			if !set {
				b.WriteString(" not set |")
				continue
			}
			fmt.Fprintf(&b, " %s |", presetCell(value, "empty"))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// presetCell renders a value verbatim in code formatting, or the given
// word for an empty one. A pipe would end the table cell even inside
// backticks, so it is escaped.
func presetCell(value, empty string) string {
	if value == "" {
		return "*" + empty + "*"
	}
	return "`" + strings.ReplaceAll(value, "|", `\|`) + "`"
}

// referenceURL links a dotted key to its entry in the generated config
// reference: nss.attributes.shell lives on the nss/attributes page, under
// the shell heading.
func referenceURL(key string) string {
	parts := strings.Split(key, ".")
	page := strings.Join(parts[:len(parts)-1], "/")
	return "/ssoossh/reference/config/" + page + "/#" + parts[len(parts)-1]
}

// checkPresetDocs fails when a key some preset sets does not mention
// ldap.schema in its doc comment, so the reference entry for that key
// cannot fall silent about the preset that may change it.
func checkPresetDocs(sections []*confdocs.Section) error {
	_, fields := fieldIndex(sections)
	var missing []string
	for _, schema := range config.LDAPSchemas() {
		settings, err := config.LDAPSchemaPresetSettings(schema)
		if err != nil {
			return err
		}
		for _, s := range settings {
			f, ok := fields[s.Key]
			if !ok || !strings.Contains(strings.Join(f.Doc, " "), presetNote) {
				missing = append(missing, s.Key)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("keys an ldap.schema preset sets do not say so in their doc comments: %s\n"+
		"Add \"A preset from ldap.schema may set this.\" to each in server/config", strings.Join(slices.Compact(slices.Sorted(slices.Values(missing))), ", "))
}

// fieldIndex maps every dotted key to its declaration position and field.
func fieldIndex(sections []*confdocs.Section) (map[string]int, map[string]*confdocs.Field) {
	order := map[string]int{}
	fields := map[string]*confdocs.Field{}
	var walk func(fs []*confdocs.Field)
	walk = func(fs []*confdocs.Field) {
		for _, f := range fs {
			if f.Path != "" {
				order[f.Path] = len(order)
				fields[f.Path] = f
			}
			walk(f.Children)
		}
	}
	for _, s := range sections {
		walk(s.Fields)
	}
	return order, fields
}
