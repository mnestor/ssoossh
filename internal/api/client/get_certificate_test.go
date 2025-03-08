// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"testing"

	"resty.dev/v3"
)

func TestClient_GetCertificate(t *testing.T) {
	type fields struct {
		Request *resty.Request
		Server  string
	}
	type args struct {
		id string
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
			got, err := c.GetCertificate(tt.args.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("Client.GetCertificate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Client.GetCertificate() = %v, want %v", got, tt.want)
			}
		})
	}
}
