package cmd

import (
	"testing"

	"github.com/bep/simplecobra"
)

// commanderNames returns the Name() of each child Commander, for asserting
// the CLI surface documented in docs/man/ssoossh.1.
func commanderNames(cmds []simplecobra.Commander) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name()
	}
	return names
}

func TestCommandGroupsExposeExpectedSurface(t *testing.T) {
	tests := []struct {
		name     string
		group    simplecobra.Commander
		wantSubs []string
	}{
		{name: "ssh", group: newSSHCommand(), wantSubs: []string{"login", "logout", "proxycommand", "inspect", "config"}},
		{name: "host", group: newHostCommand(), wantSubs: []string{"principals", "mapping"}},
		{name: "service", group: newServiceCommand(), wantSubs: []string{"enroll", "retrieve"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.group.Name(); got != tt.name {
				t.Fatalf("expected Name() %q, got %q", tt.name, got)
			}
			got := commanderNames(tt.group.Commands())
			if len(got) != len(tt.wantSubs) {
				t.Fatalf("expected subcommands %v, got %v", tt.wantSubs, got)
			}
			for i, want := range tt.wantSubs {
				if got[i] != want {
					t.Fatalf("expected subcommand %d to be %q, got %q", i, want, got[i])
				}
			}
		})
	}
}

func TestRootCommandsExposeExpectedSurface(t *testing.T) {
	root := &RootCommand{
		commands: []simplecobra.Commander{
			newCACommand(),
			newSSHCommand(),
			newHostCommand(),
			newServiceCommand(),
			newVersionCommand(),
		},
	}

	want := []string{"ca", "ssh", "host", "service", "version"}
	got := commanderNames(root.Commands())
	if len(got) != len(want) {
		t.Fatalf("expected commands %v, got %v", want, got)
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("expected command %d to be %q, got %q", i, name, got[i])
		}
	}
}
