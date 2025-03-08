// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"reflect"
	"testing"
)

func TestNewKeyPair(t *testing.T) {
	type args struct {
		keyTypeRSA bool
		keyTypeEC  bool
		keySize    int
		t          string
	}
	tests := []struct {
		name    string
		args    args
		want    *KeyPair
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewKeyPair(tt.args.keyTypeRSA, tt.args.keyTypeEC, tt.args.keySize, tt.args.t)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewKeyPair() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewKeyPair() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKeyPair_String(t *testing.T) {
	tests := []struct {
		name string
		k    *KeyPair
		want string
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.k.String(); got != tt.want {
				t.Errorf("KeyPair.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKeyPair_ParseCertificate(t *testing.T) {
	type args struct {
		c string
	}
	tests := []struct {
		name    string
		k       *KeyPair
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.k.ParseCertificate(tt.args.c); (err != nil) != tt.wantErr {
				t.Errorf("KeyPair.ParseCertificate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
