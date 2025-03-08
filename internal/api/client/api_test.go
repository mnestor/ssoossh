// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"reflect"
	"testing"

	"resty.dev/v3"
)

func TestClient_getApiPath(t *testing.T) {
	type fields struct {
		Request *resty.Request
		Server  string
	}
	type args struct {
		p string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				Request: tt.fields.Request,
				Server:  tt.fields.Server,
			}
			if got := c.getApiPath(tt.args.p); got != tt.want {
				t.Errorf("Client.getApiPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetClient(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want *Client
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetClient(tt.args.s); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetClient() = %v, want %v", got, tt.want)
			}
		})
	}
}
