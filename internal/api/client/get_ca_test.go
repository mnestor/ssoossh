// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"testing"

	"resty.dev/v3"
)

func TestClient_GetCA(t *testing.T) {
	type fields struct {
		Request *resty.Request
		Server  string
	}
	tests := []struct {
		name    string
		fields  fields
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
			got, err := c.GetCA()
			if (err != nil) != tt.wantErr {
				t.Errorf("Client.GetCA() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Client.GetCA() = %v, want %v", got, tt.want)
			}
		})
	}
}
