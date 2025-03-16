// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"bytes"
	"context"
	"testing"

	api "github.com/mnestor/ssoossh/internal/api/client"
)

func Test_caRun(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantO   string
		wantE   string
		wantErr bool
		api     api.ClientI
	}{
		{
			name: "ca from config file",
			args: []string{
				"ca",
				"--config",
				"testdata/ssoossh.ca.yaml",
				"--server",
				"nothing",
			},
			wantO:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFoeGjSXgh1UVJ4UwsYeMfB33yrTXDN589O0tT2Cp1UX\n",
			wantE:   "",
			wantErr: false,
			api:     nil,
		},
		{
			name: "ca from api",
			args: []string{
				"ca",
				"--server",
				"nothing",
			},
			wantO:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFoeGjSXgh1UVJ4UwsYeMfB33yrTXDN589O0tT2Cp1UX\n",
			wantE:   "",
			wantErr: false,
			api:     new(MockApiClientGood),
		},
		{
			name: "ca from api fail",
			args: []string{
				"ca",
				"--server",
				"nothing",
			},
			wantO:   "",
			wantE:   "unable to talk to server please check your configuration",
			wantErr: true,
			api:     new(MockApiClientFail),
		},
		{
			name: "ca from api uhh",
			args: []string{
				"ca",
				"--server",
				"nothing",
			},
			wantO:   "Unable to get CA from server: nothing\n",
			wantE:   "",
			wantErr: false,
			api:     new(MockApiClientUhh),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &bytes.Buffer{}
			e := &bytes.Buffer{}

			// Test the function
			ctx := context.Background()
			ctx = context.WithValue(ctx, APICLIENT_CTX, tt.api)
			cmd, err := NewRootCommand(ctx, o, e, tt.args)

			if err != nil && tt.wantErr && tt.wantE != err.Error() {
				t.Errorf("expected error want: %s, got %s", tt.wantE, err.Error())
				return
			}

			_, err = cmd.ExecuteContextC(ctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("caRun() error = %v, wantErr %v", e.String(), tt.wantErr)
			}

			if o.String() != tt.wantO {
				t.Errorf("caRun() wantedO = [%v], got [%v]", tt.wantO, o.String())
			}
		})
	}
}
