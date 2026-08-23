package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// findCommand walks a cobra tree by command path, e.g. "ssh", "login".
// Returns nil if any step is missing.
func findCommand(root *cobra.Command, path ...string) *cobra.Command {
	cur := root
	for _, name := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == name {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func TestCobraCommandForManpage_ShouldReturnTheRealRootWhenCalled(t *testing.T) {
	root, err := CobraCommandForManpage()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if root == nil {
		t.Fatal("expected a non-nil root command")
	}
	if root.Name() != "ssoossh" {
		t.Errorf("expected root name %q, got %q", "ssoossh", root.Name())
	}
}

// The whole point of generating from the real tree: every command a user can
// type must appear, at every depth. The hand-built tree this replaced had
// five top-level stubs and nothing below them, which is how `host` came to be
// documented as "Manage host certificates" long after host certificates were
// removed.
func TestCobraCommandForManpage_ShouldExposeEveryCommandInTheTree(t *testing.T) {
	root, err := CobraCommandForManpage()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	paths := [][]string{
		{"ca"},
		{"version"},
		{"ssh"},
		{"ssh", "login"},
		{"ssh", "logout"},
		{"ssh", "proxycommand"},
		{"ssh", "inspect"},
		{"ssh", "config"},
		{"host"},
		{"host", "principals"},
		{"host", "mapping"},
		{"host", "mapping", "list"},
		{"host", "mapping", "add"},
		{"host", "mapping", "remove"},
		{"service"},
		{"service", "enroll"},
		{"service", "retrieve"},
	}

	for _, path := range paths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			if findCommand(root, path...) == nil {
				t.Errorf("expected %q to be present in the man page tree", strings.Join(path, " "))
			}
		})
	}
}

// Flags are the other half of what the hand-built page dropped: --verbose was
// added and never appeared, and no leaf flag ever did.
func TestCobraCommandForManpage_ShouldCarryFlagsAtEveryLevel(t *testing.T) {
	root, err := CobraCommandForManpage()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name string
		path []string
		flag string
		// persistent marks a flag registered on PersistentFlags, which is
		// where a parent's flags live before cobra merges them into a leaf.
		persistent bool
	}{
		{name: "root config", path: nil, flag: "config", persistent: true},
		{name: "root server", path: nil, flag: "server", persistent: true},
		{name: "root verbose", path: nil, flag: "verbose", persistent: true},
		{name: "root debug", path: nil, flag: "debug", persistent: true},
		{name: "login force", path: []string{"ssh", "login"}, flag: "force"},
		{name: "login key-type", path: []string{"ssh", "login"}, flag: "key-type"},
		{name: "login key-size", path: []string{"ssh", "login"}, flag: "key-size"},
		{name: "login no-pty", path: []string{"ssh", "login"}, flag: "no-pty"},
		{name: "login no-agent-forwarding", path: []string{"ssh", "login"}, flag: "no-agent-forwarding"},
		{name: "login no-port-forwarding", path: []string{"ssh", "login"}, flag: "no-port-forwarding"},
		{name: "login no-x11-forwarding", path: []string{"ssh", "login"}, flag: "no-x11-forwarding"},
		{name: "login no-user-rc", path: []string{"ssh", "login"}, flag: "no-user-rc"},
		{name: "host file", path: []string{"host"}, flag: "file", persistent: true},
		{name: "enroll key", path: []string{"service", "enroll"}, flag: "key"},
		{name: "enroll retrieve", path: []string{"service", "enroll"}, flag: "retrieve"},
		{name: "retrieve code", path: []string{"service", "retrieve"}, flag: "code"},
		{name: "retrieve key", path: []string{"service", "retrieve"}, flag: "key"},
		{name: "retrieve force", path: []string{"service", "retrieve"}, flag: "force"},
		{name: "retrieve grace", path: []string{"service", "retrieve"}, flag: "grace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := findCommand(root, tt.path...)
			if cmd == nil {
				t.Fatalf("expected %q to be present", strings.Join(tt.path, " "))
			}
			set := cmd.Flags()
			if tt.persistent {
				set = cmd.PersistentFlags()
			}
			if set.Lookup(tt.flag) == nil {
				t.Errorf("expected %q to carry --%s", strings.Join(tt.path, " "), tt.flag)
			}
		})
	}
}

// The root's help text is what becomes the man page's DESCRIPTION. Asserting
// it is non-empty and came from RootCommand.Init (rather than a copy) is the
// cheapest guard against the two drifting apart again.
func TestCobraCommandForManpage_ShouldCarryTheRootsOwnHelpText(t *testing.T) {
	root, err := CobraCommandForManpage()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if root.Short == "" {
		t.Error("expected the root to carry a Short description")
	}
	if !strings.Contains(root.Long, "ssh_config") {
		t.Errorf("expected the root Long description to come from RootCommand.Init, got %q", root.Long)
	}
}

// A man page build must not read the user's configuration, dial an
// ssh-agent, or reach the network — `make gendocs` runs on build machines
// with none of those, and an earlier version of this function reached
// GetCA over the network purely to get at the assembled tree.
//
// The property reduces to something directly checkable: the docs root is
// built with all four PreRun seams nil, so if PreRun ever ran again it
// would nil-panic here rather than quietly succeed on a developer machine
// that happens to have a config file, an agent, and a reachable server.
func TestManpageRoot_ShouldLeavePreRunSeamsNil(t *testing.T) {
	root, err := newManpageRoot()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	seams := map[string]bool{
		"newConfig":    root.newConfig == nil,
		"newAPIClient": root.newAPIClient == nil,
		"newSSHAgent":  root.newSSHAgent == nil,
		"newFileAgent": root.newFileAgent == nil,
	}
	for name, isNil := range seams {
		if !isNil {
			t.Errorf("expected %s to be nil so PreRun cannot run during man page generation", name)
		}
	}
}

// The tree has to be fully assembled by simplecobra.New alone, without
// Execute — that is what makes the nil seams above safe.
func TestManpageRoot_ShouldCaptureTheCobraRootDuringInit(t *testing.T) {
	root, err := newManpageRoot()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if root.cobraRoot == nil {
		t.Fatal("expected Init to have captured the assembled cobra root")
	}
	if got := len(root.cobraRoot.Commands()); got != 5 {
		t.Errorf("expected 5 top-level commands on the captured root, got %d", got)
	}
}
