// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"testing"

	"github.com/mnestor/ssoossh/internal/ssh"
	"resty.dev/v3"
)

func TestClient_PostPubKey(t *testing.T) {
	type fields struct {
		Request *resty.Request
		Server  string
	}
	type args struct {
		kp *ssh.KeyPair
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				Request: tt.fields.Request,
				Server:  tt.fields.Server,
			}
			got, err := c.PostPubKey(tt.args.kp)
			if (err != nil) != tt.wantErr {
				t.Errorf("Client.PostPubKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Client.PostPubKey() = %v, want %v", got, tt.want)
			}
		})
	}
}
