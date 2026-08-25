package confdocs

import (
	"fmt"
	"os"
	"strings"
)

// Markers bounding the generated region of a file. Everything outside them is
// hand-written: the man page's NAME, DESCRIPTION, LOCATION, NOTES,
// ENVIRONMENT, and SEE ALSO sections are prose no struct can produce.
const (
	ManBegin = `.\" BEGIN GENERATED OPTIONS`
	ManEnd   = `.\" END GENERATED OPTIONS`
)

// RequireDocs fails when a configuration key has no doc comment. The struct is
// the only source of prose for this key in the man page and in defaults.yaml,
// so an undocumented field would silently ship as a bare name in both.
func RequireDocs(sections []*Section) error {
	var missing []string
	var walk func(fields []*Field)
	walk = func(fields []*Field) {
		for _, f := range fields {
			if !f.IsStruct() && !f.Embedded && len(f.Doc) == 0 {
				missing = append(missing, f.Path)
			}
			walk(f.Children)
		}
	}
	for _, s := range sections {
		walk(s.Fields)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("config fields have no doc comment, so they would ship undocumented: %s\n"+
		"Add a doc comment to each in server/config", strings.Join(missing, ", "))
}

// WriteManPage splices the generated OPTIONS body into path, replacing only
// the region between the markers. Reports whether the file changed.
func WriteManPage(path string, sections []*Section, defaults *Defaults) (bool, error) {
	before, err := os.ReadFile(path) //nolint:gosec // a repo path passed by the generator
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	body := ManPage(sections, defaults)
	next, err := spliceMarkers(string(before), ManBegin, ManEnd, body)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if next == string(before) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// spliceMarkers replaces the lines between begin and end with body, keeping
// the marker lines themselves.
func spliceMarkers(src, begin, end, body string) (string, error) {
	lines := strings.Split(src, "\n")

	from, to := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case begin:
			from = i
		case end:
			to = i
		}
	}
	if from < 0 || to < 0 {
		return "", fmt.Errorf("missing generated-region markers %q and %q", begin, end)
	}
	if to < from {
		return "", fmt.Errorf("marker %q appears before %q", end, begin)
	}

	var out []string
	out = append(out, lines[:from+1]...)
	out = append(out, strings.Split(strings.TrimRight(body, "\n"), "\n")...)
	out = append(out, lines[to:]...)
	return strings.Join(out, "\n"), nil
}
