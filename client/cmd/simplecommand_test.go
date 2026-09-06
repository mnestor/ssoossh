package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
)

func TestSimpleCommandRun(t *testing.T) {
	tests := []struct {
		name    string
		initErr error
		run     func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error
		wantErr error
		wantRan bool
	}{
		{
			name:    "should surface root InitErr without calling run when init failed",
			initErr: errors.New("init failed"),
			run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
				t.Fatal("run should not be called when InitErr is set")
				return nil
			},
			wantErr: errors.New("init failed"),
		},
		{
			name: "should call run when init succeeded",
			run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
				return nil
			},
			wantRan: true,
		},
		{
			name:    "should no-op for group commands with a nil run",
			run:     nil,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &RootCommand{initErr: tt.initErr}
			rootCd := &simplecobra.Commandeer{Command: root}
			rootCd.Root = rootCd

			c := &simpleCommand{name: "test", run: tt.run}
			cd := &simplecobra.Commandeer{Command: c, Root: rootCd, CobraCommand: &cobra.Command{}}

			err := c.Run(context.Background(), cd, nil)

			if tt.wantErr != nil {
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestSimpleCommandInit(t *testing.T) {
	c := &simpleCommand{name: "test", short: "short desc", long: "long desc"}
	cd := &simplecobra.Commandeer{CobraCommand: &cobra.Command{}}

	if err := c.Init(cd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cd.CobraCommand.Short != "short desc" {
		t.Fatalf("expected Short to be set, got %q", cd.CobraCommand.Short)
	}
	if cd.CobraCommand.Long != "long desc" {
		t.Fatalf("expected Long to be set, got %q", cd.CobraCommand.Long)
	}
}

// TestSimpleCommandInit_ShouldNameThePositionalArgumentsInTheUsageLine
// covers the help gap this field exists for: simplecobra names every leaf
// "<name> [flags] [args]", so `host mapping add --help` said nothing about
// the two arguments it requires and the only way to learn them was to run
// the command wrong.
func TestSimpleCommandInit_ShouldNameThePositionalArgumentsInTheUsageLine(t *testing.T) {
	tests := []struct {
		name    string
		argSpec string
		wantUse string
	}{
		{
			name:    "should name required arguments",
			argSpec: "<account> <principal>",
			wantUse: "test <account> <principal>",
		},
		{
			name:    "should name an optional argument",
			argSpec: "<account> [principal]",
			wantUse: "test <account> [principal]",
		},
		{
			name:    "should leave the default usage line alone when a command takes no arguments",
			argSpec: "",
			wantUse: "test [flags] [args]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &simpleCommand{name: "test", argSpec: tt.argSpec}
			cd := &simplecobra.Commandeer{CobraCommand: &cobra.Command{Use: "test [flags] [args]"}}

			if err := c.Init(cd); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := cd.CobraCommand.Use; got != tt.wantUse {
				t.Errorf("Use = %q, want %q", got, tt.wantUse)
			}
		})
	}
}

// The message for calling a command wrong is built from the same argSpec
// the usage line uses, so the two cannot drift and neither hard-codes the
// binary name.
func TestSimpleCommandUsageError_ShouldMatchTheUsageLine(t *testing.T) {
	c := &simpleCommand{name: "add", argSpec: "<account> <principal>"}
	root := &cobra.Command{Use: "ssoossh"}
	leaf := &cobra.Command{Use: "add"}
	root.AddCommand(leaf)
	cd := &simplecobra.Commandeer{CobraCommand: leaf}

	err := c.usageError(cd)

	if err == nil || err.Error() != "usage: ssoossh add <account> <principal>" {
		t.Errorf("got %v, want the command path and the argument spec", err)
	}
}
