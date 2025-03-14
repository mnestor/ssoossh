// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"bytes"
	"context"
	"testing"
)

func Test_run(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantO   string
		wantE   string
		wantErr bool
	}{
		{
			name:    "flag file require value",
			args:    []string{"--config"},
			wantO:   "",
			wantE:   "Error: flag needs an argument: --config\n",
			wantErr: true,
		},
		{
			name:    "flag file require value",
			args:    []string{"--config", "test"},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
		{
			name:    "flag server require value",
			args:    []string{"--server"},
			wantO:   "",
			wantE:   "Error: flag needs an argument: --server\n",
			wantErr: true,
		},
		{
			name:    "flag server require value",
			args:    []string{"--server", "test"},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &bytes.Buffer{}
			e := &bytes.Buffer{}

			// Test the function
			cmd, err := NewRootCommand(context.Background(), o, e, tt.args)
			if err != nil && tt.wantErr && tt.wantE != err.Error() {
				t.Errorf("expected error want: %s, got %s", tt.wantE, err.Error())
				return
			}
			_ = cmd.Execute()
			if tt.wantErr && e.String() != tt.wantE {
				t.Errorf("Error wanted: %s, got: %s: %s", tt.wantE, e.String(), o.String())
			}
		})
	}
}
