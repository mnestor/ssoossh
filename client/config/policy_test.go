package config

import (
	"reflect"
	"testing"
)

func TestBuildPolicyMap_ShouldNestTheSSHKeyFields(t *testing.T) {
	flat := map[string]any{
		"server":      "https://ssh.example.com",
		"sshkey.type": "ecdsa",
		"sshkey.size": 384,
	}

	got := buildPolicyMap(flat)

	want := map[string]any{
		"server": "https://ssh.example.com",
		"sshkey": map[string]any{
			"type": "ecdsa",
			"size": 384,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestBuildPolicyMap_ShouldLeaveFlatKeysUnnested(t *testing.T) {
	flat := map[string]any{
		"insecure_skip_verify": true,
		"fips":                 false,
	}

	got := buildPolicyMap(flat)

	if got["insecure_skip_verify"] != true || got["fips"] != false {
		t.Errorf("got %#v, want the flat keys preserved as-is", got)
	}
	if _, ok := got["sshkey"]; ok {
		t.Error("did not expect an sshkey entry when neither sshkey field was set")
	}
}

func TestBuildPolicyMap_ShouldReturnAnEmptyMapForNoInput(t *testing.T) {
	got := buildPolicyMap(map[string]any{})
	if len(got) != 0 {
		t.Errorf("got %#v, want an empty map", got)
	}
}
