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
