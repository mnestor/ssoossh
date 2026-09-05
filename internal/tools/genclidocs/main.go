// Command genclidocs renders the documentation site's CLI reference from the
// cobra command trees of ssoossh and ssoosshd.
//
// One page per command, under user-docs/src/content/docs/reference/cli/, and
// user-docs/cli-sidebar.json declaring their sidebar order so the site keeps
// the command tree's own order rather than sorting alphabetically. It walks
// the same trees `ssoossh` and `ssoosshd` execute -- the ones gendocs turns
// into man pages -- so a flag or subcommand added anywhere below appears on
// the site without anyone remembering to mirror it.
//
// Usage: genclidocs [-check]
//
// Run it from the repository root; -check exits non-zero if any page or the
// sidebar would change, without writing, which is what `make clidocs-check`
// asserts.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	clientcmd "github.com/mnestor/ssoossh/client/cmd"
	servercmd "github.com/mnestor/ssoossh/server/cmd"
)

const (
	siteDir    = "user-docs/src/content/docs/reference/cli"
	sidebarOut = "user-docs/cli-sidebar.json"
	// sitePrefix is the deployed site's base path plus the CLI reference's
	// directory, which every link on a generated page is written against.
	sitePrefix = "/ssoossh/reference/cli"
	eyebrow    = "CLI reference"
)

// tree is one command root to render, with the label its sidebar group
// carries and the source directory named in each page's generated marker.
type tree struct {
	root   *cobra.Command
	label  string
	source string
}

// sidebarItem is one entry of cli-sidebar.json: a leaf with a slug, or a
// group with items. It mirrors the shape astro.config.mjs spreads into
// Starlight's sidebar, and config-sidebar.json uses the same one.
type sidebarItem struct {
	Label string        `json:"label"`
	Slug  string        `json:"slug,omitempty"`
	Items []sidebarItem `json:"items,omitempty"`
}

func main() {
	check := flag.Bool("check", false, "report whether any page would change, without writing")
	flag.Parse()

	if err := run(*check); err != nil {
		log.Fatal(err)
	}
}

// run renders both trees and either writes the result or, with check set,
// reports whether it differs from what is on disk.
func run(check bool) error {
	clientRoot, err := clientcmd.CobraCommandForManpage()
	if err != nil {
		return fmt.Errorf("build client command tree: %w", err)
	}
	serverRoot := servercmd.NewCommand().Command()
	// not covered: a defensive check that cannot trip, because
	// servercmd.NewCommand always returns a wrapper holding a non-nil
	// cobra.Command.
	if serverRoot == nil {
		return fmt.Errorf("get cobra command from server Command wrapper")
	}

	trees := []tree{
		{root: clientRoot, label: "ssoossh (client)", source: "client/cmd"},
		{root: serverRoot, label: "ssoosshd (server)", source: "server/cmd"},
	}

	pages, sidebar, err := render(trees)
	if err != nil {
		return err
	}
	stale, err := apply(siteDir, sidebarOut, pages, sidebar, check)
	if err != nil {
		return err
	}
	if check && len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "stale, run `make clidocs`: %s\n", strings.Join(stale, ", "))
		os.Exit(1)
	}
	return nil
}

// render produces every page, keyed by path relative to siteDir, and the
// sidebar JSON for the given trees.
func render(trees []tree) (map[string][]byte, []byte, error) {
	pages := map[string][]byte{}
	var sidebar []sidebarItem
	for _, t := range trees {
		items := walk(t.root, t.root, t.source, pages)
		sidebar = append(sidebar, sidebarItem{Label: t.label, Items: items})
	}
	out, err := json.MarshalIndent(sidebar, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("encode sidebar: %w", err)
	}
	return pages, append(out, '\n'), nil
}

// walk renders cmd and every available command below it into pages, and
// returns the sidebar items for cmd's group: an Overview entry for cmd
// itself followed by one entry (leaf or nested group) per subcommand.
func walk(root, cmd *cobra.Command, source string, pages map[string][]byte) []sidebarItem {
	pages[pagePath(root, cmd)] = renderPage(root, cmd, source)

	items := []sidebarItem{{Label: "Overview", Slug: slug(root, cmd)}}
	if cmd == root {
		// The root's own page is the group's overview, and the root is
		// listed under its own name rather than "Overview" so the sidebar
		// reads as the command line does.
		items[0].Label = root.Name()
	}
	for _, sub := range availableSubcommands(cmd) {
		if sub.HasAvailableSubCommands() {
			items = append(items, sidebarItem{Label: sub.Name(), Items: walk(root, sub, source, pages)})
			continue
		}
		pages[pagePath(root, sub)] = renderPage(root, sub, source)
		items = append(items, sidebarItem{Label: sub.Name(), Slug: slug(root, sub)})
	}
	return items
}

// availableSubcommands is cmd's subcommands minus hidden, deprecated, and
// help-topic ones, which is the same filter cobra's own man page generator
// applies. Cobra keeps them in declaration order, which the site preserves.
func availableSubcommands(cmd *cobra.Command) []*cobra.Command {
	var subs []*cobra.Command
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

// pagePath is where cmd's page lives relative to siteDir: the root's name
// as a directory, then the command path below the root joined with dashes,
// or index.md for the root itself. `ssoossh ssh login` becomes
// ssoossh/ssh-login.md.
func pagePath(root, cmd *cobra.Command) string {
	if cmd == root {
		return filepath.Join(root.Name(), "index.md")
	}
	return filepath.Join(root.Name(), relativeName(root, cmd)+".md")
}

// slug is the page's Starlight slug, which is also its URL path below the
// site base.
func slug(root, cmd *cobra.Command) string {
	if cmd == root {
		return "reference/cli/" + root.Name()
	}
	return "reference/cli/" + root.Name() + "/" + relativeName(root, cmd)
}

// relativeName is cmd's command path below the root with dashes for spaces:
// `host mapping add` for `ssoossh host mapping add`.
func relativeName(root, cmd *cobra.Command) string {
	rel := strings.TrimPrefix(cmd.CommandPath(), root.CommandPath()+" ")
	return strings.ReplaceAll(rel, " ", "-")
}

// link is the absolute site URL of cmd's page, with the trailing slash the
// existing pages use.
func link(root, cmd *cobra.Command) string {
	return strings.Replace(slug(root, cmd), "reference/cli", sitePrefix, 1) + "/"
}

// renderPage writes one command's page: frontmatter, the generated marker,
// the description, synopsis, examples, its own options, inherited options,
// subcommands, and where it sits in the tree.
func renderPage(root, cmd *cobra.Command, source string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "---\ntitle: %q\ndescription: %q\neyebrow: %q\n---\n\n", cmd.CommandPath(), cmd.Short, eyebrow)
	fmt.Fprintf(&b, "<!-- Generated by genclidocs from the cobra command tree in %s. Do not edit. -->\n\n", source)

	description := cmd.Long
	if strings.TrimSpace(description) == "" {
		description = cmd.Short
	}
	b.WriteString(escapeProse(strings.TrimSpace(description)))
	b.WriteString("\n\n")

	b.WriteString("## Synopsis\n\n```\n")
	b.WriteString(cmd.UseLine())
	b.WriteString("\n```\n\n")

	if strings.TrimSpace(cmd.Example) != "" {
		b.WriteString("## Examples\n\n```bash\n")
		b.WriteString(strings.TrimSpace(cmd.Example))
		b.WriteString("\n```\n\n")
	}

	if local := visibleFlags(cmd.NonInheritedFlags()); len(local) > 0 {
		b.WriteString("## Options\n\n")
		writeFlagTable(&b, local)
	}
	if inherited := visibleFlags(cmd.InheritedFlags()); len(inherited) > 0 {
		b.WriteString("## Global options\n\n")
		fmt.Fprintf(&b, "Inherited from [`%s`](%s) and its parents.\n\n", root.CommandPath(), link(root, root))
		writeFlagTable(&b, inherited)
	}

	if subs := availableSubcommands(cmd); len(subs) > 0 {
		b.WriteString("## Subcommands\n\n| Command | Description |\n| --- | --- |\n")
		for _, sub := range subs {
			fmt.Fprintf(&b, "| [`%s`](%s) | %s |\n", sub.CommandPath(), link(root, sub), cell(sub.Short))
		}
		b.WriteString("\n")
	}

	if cmd != root {
		b.WriteString("## See also\n\n")
		fmt.Fprintf(&b, "- [`%s`](%s)", cmd.Parent().CommandPath(), link(root, cmd.Parent()))
		if cmd.Parent() != root {
			fmt.Fprintf(&b, "\n- [`%s`](%s)", root.CommandPath(), link(root, root))
		}
		b.WriteString("\n")
	}
	return b.Bytes()
}

// visibleFlags returns the set's flags minus hidden and deprecated ones,
// sorted by name, which is the order `--help` prints them in.
func visibleFlags(set *pflag.FlagSet) []*pflag.Flag {
	var flags []*pflag.Flag
	set.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		flags = append(flags, f)
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// writeFlagTable emits one row per flag: how it is spelled, its value type,
// its default, and its usage text.
func writeFlagTable(b *bytes.Buffer, flags []*pflag.Flag) {
	b.WriteString("| Flag | Type | Default | Description |\n| --- | --- | --- | --- |\n")
	for _, f := range flags {
		name := "`--" + f.Name + "`"
		if f.Shorthand != "" {
			name = "`-" + f.Shorthand + "`, " + name
		}
		def := ""
		if f.DefValue != "" && f.DefValue != "[]" {
			def = "`" + f.DefValue + "`"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", name, f.Value.Type(), def, cell(f.Usage))
	}
	b.WriteString("\n")
}

// cell makes free text safe inside a markdown table cell: pipes escaped,
// newlines collapsed, angle brackets escaped so `<path>` is not read as HTML.
func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return escapeAngles(s)
}

// escapeProse escapes angle brackets in every line of s that is not inside
// a fenced code block, so a placeholder like <server-url> in a command's
// long description survives markdown rendering instead of being dropped as
// an unknown HTML tag.
func escapeProse(s string) string {
	lines := strings.Split(s, "\n")
	inFence := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			lines[i] = escapeAngles(line)
		}
	}
	return strings.Join(lines, "\n")
}

// escapeAngles replaces < and > outside inline code spans with entities.
func escapeAngles(s string) string {
	var b strings.Builder
	inCode := false
	for _, r := range s {
		switch {
		case r == '`':
			inCode = !inCode
			b.WriteRune(r)
		case !inCode && r == '<':
			b.WriteString("&lt;")
		case !inCode && r == '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// apply reconciles the rendered pages and sidebar with what is on disk under
// dir and at sidebarPath. It returns the paths that differ, sorted. With
// check set nothing is written; otherwise changed pages are written, pages
// the trees no longer produce are removed, and the sidebar is rewritten if
// it changed.
func apply(dir, sidebarPath string, pages map[string][]byte, sidebar []byte, check bool) ([]string, error) {
	removed, err := removeStale(dir, pages, check)
	if err != nil {
		return nil, err
	}
	written, err := writePages(dir, pages, check)
	if err != nil {
		return nil, err
	}
	stale := append(removed, written...)

	current, err := os.ReadFile(sidebarPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", sidebarPath, err)
	}
	if !bytes.Equal(current, sidebar) {
		stale = append(stale, sidebarPath)
		if !check {
			if err := os.WriteFile(sidebarPath, sidebar, 0o600); err != nil {
				return nil, fmt.Errorf("write %s: %w", sidebarPath, err)
			}
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// removeStale deletes every file under dir that pages no longer produces,
// or with check set only names them, so a renamed command does not leave
// its old page behind.
func removeStale(dir string, pages map[string][]byte, check bool) ([]string, error) {
	existing, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	var stale []string
	for rel := range existing {
		if _, keep := pages[rel]; keep {
			continue
		}
		stale = append(stale, filepath.Join(dir, rel))
		if !check {
			if err := os.Remove(filepath.Join(dir, rel)); err != nil {
				return nil, fmt.Errorf("remove stale page: %w", err)
			}
		}
	}
	return stale, nil
}

// writePages writes every page whose content differs from what is on disk,
// or with check set only names them. Pages are visited in sorted order so
// the report is stable.
func writePages(dir string, pages map[string][]byte, check bool) ([]string, error) {
	rels := make([]string, 0, len(pages))
	for rel := range pages {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	var stale []string
	for _, rel := range rels {
		path := filepath.Join(dir, rel)
		current, err := os.ReadFile(path)
		if err == nil && bytes.Equal(current, pages[rel]) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		stale = append(stale, path)
		if check {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, pages[rel], 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
	}
	return stale, nil
}

// listFiles returns every regular file below dir, keyed by path relative to
// dir. A missing dir is an empty set, not an error: the first run creates it.
func listFiles(dir string) (map[string]struct{}, error) {
	files := map[string]struct{}{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[rel] = struct{}{}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	return files, nil
}
