package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
)

func TestSimpleCommandRun(t *testing.T) {
	tests := []struct {
		name       string
		initErr    error
		configFree bool
		run        func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error
		wantErr    error
		wantRan    bool
		wantStderr string
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
		{
			name:       "should call run when init failed and the command reads no configuration",
			initErr:    errors.New("load config: broken"),
			configFree: true,
			run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
				return nil
			},
			wantRan: true,
		},
		{
			name:       "should warn on stderr when init failed and the command reads no configuration",
			initErr:    errors.New("load config: broken"),
			configFree: true,
			run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
				return nil
			},
			wantStderr: "Warning: load config: broken; continuing without the configuration\n",
		},
		{
			name:       "should surface a broken --config file even when the command reads no configuration",
			initErr:    fmt.Errorf("load config: %w", &config.ExplicitConfigError{Path: "x.yaml", Reason: "does not exist"}),
			configFree: true,
			run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
				t.Fatal("run should not be called when the --config file is broken")
				return nil
			},
			wantErr: errors.New(`load config: the config file "x.yaml" given with --config does not exist`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &RootCommand{initErr: tt.initErr}
			rootCd := &simplecobra.Commandeer{Command: root}
			rootCd.Root = rootCd

			ran := false
			run := tt.run
			if run != nil {
				run = func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
					ran = true
					return tt.run(ctx, cd, root, args)
				}
			}
			c := &simpleCommand{name: "test", run: run, configFree: tt.configFree}
			var stderr bytes.Buffer
			cobraCmd := &cobra.Command{}
			cobraCmd.SetErr(&stderr)
			cd := &simplecobra.Commandeer{Command: c, Root: rootCd, CobraCommand: cobraCmd}

			err := c.Run(context.Background(), cd, nil)

			if tt.wantRan && !ran {
				t.Fatal("expected run to be called")
			}
			if tt.wantStderr != "" && stderr.String() != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantStderr)
			}

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
			name:    "should name no arguments when a command takes none",
			argSpec: "",
			wantUse: "test",
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

// TestSimpleCommandCheckArity pins the message for each way of passing the
// wrong number of arguments. It is built from the same argSpec the usage
// line uses, so the two cannot drift and neither hard-codes the binary name.
func TestSimpleCommandCheckArity(t *testing.T) {
	tests := []struct {
		name    string
		argSpec string
		args    []string
		want    string
	}{
		{name: "should accept the required arguments", argSpec: "<account> <principal>", args: []string{"deploy", "alice"}},
		{name: "should accept a missing optional argument", argSpec: "<account> [principal]", args: []string{"deploy"}},
		{name: "should name the first missing argument", argSpec: "<account> <principal>", args: []string{"deploy"}, want: "missing <principal>; usage: ssoossh add <account> <principal>"},
		{name: "should name the first unexpected argument", argSpec: "<username>", args: []string{"alice", "bob"}, want: `unexpected argument "bob"; usage: ssoossh add <username>`},
		{name: "should refuse any argument to a command that takes none", argSpec: "", args: []string{"extra"}, want: `ssoossh add takes no arguments, got "extra"`},
		{name: "should accept any number after a variadic argument", argSpec: "<command> [args...]", args: []string{"nc", "-v", "host", "22"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &simpleCommand{name: "add", argSpec: tt.argSpec}
			root := &cobra.Command{Use: "ssoossh"}
			leaf := &cobra.Command{Use: "add"}
			root.AddCommand(leaf)

			err := c.checkArity(leaf, tt.args)

			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestArity(t *testing.T) {
	tests := []struct {
		spec             string
		wantMin, wantMax int
	}{
		{spec: "", wantMin: 0, wantMax: 0},
		{spec: "<username>", wantMin: 1, wantMax: 1},
		{spec: "<account> [principal]", wantMin: 1, wantMax: 2},
		{spec: "<command> [args...]", wantMin: 1, wantMax: -1},
	}
	for _, tt := range tests {
		t.Run("should read "+tt.spec, func(t *testing.T) {
			if gotMin, gotMax := arity(tt.spec); gotMin != tt.wantMin || gotMax != tt.wantMax {
				t.Errorf("arity(%q) = %d, %d, want %d, %d", tt.spec, gotMin, gotMax, tt.wantMin, tt.wantMax)
			}
		})
	}
}
