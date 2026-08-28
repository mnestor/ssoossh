package confdocs

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults holds the values from server/config/defaults.yaml, keyed by dotted
// path.
//
// The default: tags on the config structs are what decide these values;
// WriteDefaults renders them into the file, and this type reads the file
// back so the man page states each key's default from the same artifact
// viper loads and /etc/ssoossh ships. Reading the generated file rather
// than the tags directly keeps the man page honest about what actually
// shipped: it can never describe a tag that the yaml pass dropped.
type Defaults struct {
	values map[string]string
	// present records which paths defaults.yaml sets at all, so a key it
	// leaves out can be reported as the Go zero value rather than as blank.
	present map[string]bool
}

// LoadDefaults reads and flattens defaults.yaml.
func LoadDefaults(path string) (*Defaults, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var root map[string]any
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	d := &Defaults{values: map[string]string{}, present: map[string]bool{}}
	var walk func(prefix string, node any)
	walk = func(prefix string, node any) {
		switch v := node.(type) {
		case map[string]any:
			for key, child := range v {
				next := key
				if prefix != "" {
					next = prefix + "." + key
				}
				walk(next, child)
			}
		case []any:
			d.set(prefix, renderList(v))
		case nil:
			d.set(prefix, "")
		default:
			d.set(prefix, fmt.Sprintf("%v", v))
		}
	}
	walk("", root)
	return d, nil
}

func (d *Defaults) set(path, value string) {
	d.values[path] = value
	d.present[path] = true
}

// renderList formats a YAML sequence for display in documentation.
func renderList(items []any) string {
	if len(items) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%v", it))
	}
	return strings.Join(parts, ", ")
}

// Describe returns the default to print for a field: the value defaults.yaml
// sets, or the Go zero value for its type when the file leaves it unset,
// since that is what viper will hand the server either way.
func (d *Defaults) Describe(f *Field) string {
	if v, ok := d.values[f.Path]; ok {
		if v == "" {
			return "empty"
		}
		return v
	}
	switch f.Type {
	case "bool":
		return "false"
	case "int", "number":
		return "0"
	case "duration":
		return "0"
	case "list":
		return "empty"
	case "string":
		return "empty"
	default:
		return ""
	}
}

// Unknown returns the paths defaults.yaml sets that no config struct claims.
// A key here is either a typo or a field that was removed from the structs
// while its default was left behind, and both are worth failing on.
func (d *Defaults) Unknown(sections []*Section) []string {
	known := map[string]bool{}
	var walk func(fields []*Field)
	walk = func(fields []*Field) {
		for _, f := range fields {
			if f.Path != "" {
				known[f.Path] = true
			}
			walk(f.Children)
		}
	}
	for _, s := range sections {
		walk(s.Fields)
	}

	var out []string
	for path := range d.present {
		if known[path] {
			continue
		}
		// A struct key is known by its children, and the rotation options
		// belong to the embedded timberjack logger rather than to a field of
		// ours, so treat any path under a known prefix as accounted for.
		if hasKnownPrefix(known, path) {
			continue
		}
		out = append(out, path)
	}
	return out
}

// hasKnownPrefix reports whether path sits beneath a documented key.
func hasKnownPrefix(known map[string]bool, path string) bool {
	for {
		i := strings.LastIndex(path, ".")
		if i < 0 {
			return false
		}
		path = path[:i]
		if known[path] {
			return true
		}
	}
}
