// Command genconfdocs renders the ssoosshd configuration surface from the Go
// structs in server/config.
//
// It writes three artifacts from one walk:
//
//   - docs/man/ssoosshd.yaml.5, the OPTIONS body between the generated-region
//     markers
//   - server/config/defaults.yaml, whose comments are regenerated in place
//     while every value is left exactly as it was
//   - user-docs/src/content/docs/reference/config/, the documentation site's
//     configuration reference, one page per section (big sections split into
//     a directory of subpages), regenerated wholesale, plus
//     user-docs/config-sidebar.json declaring their sidebar order
//
// Usage: genconfdocs [-check]
//
// Run it from the repository root; -check exits non-zero if either file would
// change, which is what `make confdocs-check` asserts.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/mnestor/ssoossh/internal/tools/confdocs"
)

const (
	configPkg  = "server/config"
	tlsPkg     = "server/config/tlsutils"
	defaultsIn = "server/config/defaults.yaml"
	manPage    = "docs/man/ssoosshd.yaml.5"
	siteDir    = "user-docs/src/content/docs/reference/config"
	sidebarOut = "user-docs/config-sidebar.json"
)

func main() {
	check := flag.Bool("check", false, "report whether either file would change, without writing")
	flag.Parse()

	if err := run(*check); err != nil {
		log.Fatal(err)
	}
}

// walk reads the configuration surface out of the config structs and
// completes it with the keys the AST walk cannot see for itself.
func walk() ([]*confdocs.Section, error) {
	sections, err := confdocs.Walk([]string{configPkg, tlsPkg}, "Config")
	if err != nil {
		return nil, err
	}

	// The walk stops at a struct embedded from another module: there are no
	// doc comments of ours on its fields. Its key names still have to reach
	// defaults.yaml, so they are read off the real type here.
	keys, err := embeddedKeys()
	if err != nil {
		return nil, err
	}
	if err := confdocs.AttachEmbeddedKeys(sections, keys); err != nil {
		return nil, err
	}
	return sections, nil
}

// reportStale names the artifacts that would change and exits non-zero,
// which is what `make confdocs-check` asserts against.
func reportStale(stale map[string]bool) {
	var out []string
	for path, changed := range stale {
		if changed {
			out = append(out, path)
		}
	}
	if len(out) == 0 {
		return
	}
	sort.Strings(out)
	fmt.Fprintf(os.Stderr, "stale, run `make confdocs`: %s\n", strings.Join(out, ", "))
	os.Exit(1)
}

func run(check bool) error {
	sections, err := walk()
	if err != nil {
		return err
	}

	defaults, err := confdocs.LoadDefaults(defaultsIn)
	if err != nil {
		return err
	}

	// A default with no struct behind it is either a typo or a leftover from
	// a removed field. Either way it is a key the shipped config offers and
	// the server ignores, so it fails the run rather than being generated
	// around.
	if unknown := defaults.Unknown(sections); len(unknown) > 0 {
		return fmt.Errorf("%s sets keys no config struct declares: %v\n"+
			"Remove them, or add the field they were meant to configure", defaultsIn, unknown)
	}

	if err := confdocs.RequireDocs(sections); err != nil {
		return err
	}

	// defaults.yaml first: the man page states each key's default, and now
	// that the structs decide those, the file has to be brought up to date
	// before it is read back for them. The other order documents the values
	// from the previous run, and takes two runs to settle.
	yamlChanged, err := confdocs.WriteDefaults(defaultsIn, sections)
	if err != nil {
		return err
	}
	defaults, err = confdocs.LoadDefaults(defaultsIn)
	if err != nil {
		return err
	}
	man, err := confdocs.WriteManPage(manPage, sections, defaults)
	if err != nil {
		return err
	}
	site, err := confdocs.WriteMarkdown(siteDir, sections, defaults)
	if err != nil {
		return err
	}
	sidebar, err := confdocs.WriteSidebar(sidebarOut, sections)
	if err != nil {
		return err
	}

	if check {
		reportStale(map[string]bool{
			manPage:    man,
			defaultsIn: yamlChanged,
			siteDir:    site,
			sidebarOut: sidebar,
		})
	}
	return nil
}
