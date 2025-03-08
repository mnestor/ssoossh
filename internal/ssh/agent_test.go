// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGetAgent(t *testing.T) {
	tests := []struct {
		name    string
		want    *Agent
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetAgent()
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAgent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetAgent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgent_HasKeys(t *testing.T) {
	tests := []struct {
		name string
		a    *Agent
		want bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.HasKeys(); got != tt.want {
				t.Errorf("Agent.HasKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgent_ListCertificates(t *testing.T) {
	tests := []struct {
		name    string
		a       *Agent
		want    []*ssh.Certificate
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.ListCertificates()
			if (err != nil) != tt.wantErr {
				t.Errorf("Agent.ListCertificates() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Agent.ListCertificates() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAgent_LoadCA(t *testing.T) {
	type args struct {
		ca string
	}
	tests := []struct {
		name string
		a    *Agent
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.a.LoadCA(tt.args.ca)
		})
	}
}

func TestAgent_CleanupAgent(t *testing.T) {
	tests := []struct {
		name    string
		a       *Agent
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.a.CleanupAgent(); (err != nil) != tt.wantErr {
				t.Errorf("Agent.CleanupAgent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAgent_AddCertificate(t *testing.T) {
	type args struct {
		k *KeyPair
	}
	tests := []struct {
		name    string
		a       *Agent
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.a.AddCertificate(tt.args.k); (err != nil) != tt.wantErr {
				t.Errorf("Agent.AddCertificate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
