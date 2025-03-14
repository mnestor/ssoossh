// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func Test_loadConfig(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		configW *Config
		wantO   string
		wantE   string
		wantErr bool
	}{
		{
			name:    "server required",
			args:    []string{},
			configW: nil,
			wantO:   "",
			wantE:   "server is required",
			wantErr: true,
		},
		{
			name: "server set with args",
			args: []string{
				"--server",
				"nothing good here",
			},
			configW: nil,
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
		{
			name: "config flag set",
			args: []string{
				"ca",
				"--config",
			},
			configW: nil,
			wantO:   "",
			wantE:   "flag needs an argument: --config",
			wantErr: true,
		},
		{
			name: "config set with args",
			args: []string{
				"ca",
				"--config",
				"testdata/ssoossh.server.yaml",
			},
			configW: &Config{
				ConfigFile: "testdata/ssoossh.server.yaml",
				HostPubkey: "/etc/ssh/ssh_host_rsa_key.pub",
				Server:     "justsomething",
				KeyTypeRSA: true,
				KeySize:    4096,
			},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
		{
			name: "config defaults loaded with server arg",
			args: []string{
				"ca",
				"--server",
				"nothing",
			},
			configW: &Config{
				Server:     "nothing",
				HostPubkey: "/etc/ssh/ssh_host_rsa_key.pub",
				KeyTypeRSA: true,
				KeySize:    4096,
				WriteCert:  false,
			},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
		{
			name: "config changed",
			args: []string{
				"login",
				"--type-ec",
				"true",
				"--server",
				"nothing",
			},
			configW: &Config{
				Server:     "nothing",
				KeySize:    4096,
				KeyTypeEC:  true,
				HostPubkey: "/etc/ssh/ssh_host_rsa_key.pub",
				KeyTypeRSA: false,
				WriteCert:  false,
			},
			wantO:   "",
			wantE:   "",
			wantErr: false,
		},
		{
			name: "unable to talk to server",
			args: []string{
				"login",
				"--type-ec",
				"true",
				"--server",
				"nothing",
			},
			configW: nil,
			wantO:   "",
			wantE:   "unable to talk to server please check your configuration",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &bytes.Buffer{}
			e := &bytes.Buffer{}

			// Test the function
			ctx := context.Background()
			cmd, err := NewRootCommand(ctx, o, e, tt.args)
			if err != nil && tt.wantErr && tt.wantE != err.Error() {
				t.Errorf("expected error want: %s, got %s", tt.wantE, err.Error())
				return
			}

			rCmd, err := cmd.ExecuteContextC(ctx)

			var config *Config
			configCtx := rCmd.Context().Value(CONFIG_CTX)

			if configCtx != nil {
				config, _ = configCtx.(*Config)
			}

			if tt.configW != nil {
				if !reflect.DeepEqual(config, tt.configW) {
					t.Errorf("Error config W: %+v", tt.configW)
					t.Errorf("Error config G: %+v", config)
				}
			}

			if tt.wantErr && err != nil && err.Error() != tt.wantE {
				t.Errorf("Error wanted: %s, got: %s: %s", tt.wantE, err.Error(), o.String())
			}
		})
	}
}
