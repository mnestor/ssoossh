// Created By Mike Nestor <me@mikenestor.org>
package config

import (
	"strings"
	"testing"
)

func TestConfig_SshPubKey(t *testing.T) {
	type fields struct {
		SshKey string
	}
	tests := []struct {
		name   string
		fields fields
		want   string
		wantE  string
	}{
		{
			name: "good key",
			fields: fields{
				SshKey: `-----BEGIN OPENSSH PRIVATE KEY-----
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
-----END OPENSSH PRIVATE KEY-----`,
			},
			want:  "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCQm/GexBJx34czOpqmsnduTYy8d9Vpq7ZsDLibogqgN6LDiDdDevrzdn1sET7HnbQAsq7AAcmQCeRTlyHMmv2tULpQYRiB3HjyByUkHYcmBrpIqiiS5qIvqEmV9a5rm9MzUuJb6e41YSFXjWe8kjBrl3TeSeP3RH7d4vQEySap+laeljxMmvTtsouJqDONPi+sIU4AD0mMwO5qgQsVu5+TamDUwuEr4LlTDMEp6fmWiKmhgYB5QUId3LZtehEBDyQhbRvydi7yB9yUHUl3yCD2WRAyAEATy2+3E1y/T+eoqssZNLCKpR0E/9zU+tA/uuxtvPe9pklEOskgZvHKCYOyu1whCIyJSzQhH3ns7jZmAoiw4C1/rreb9MWWsAW7wDrBHnRvmXHD4oH7UmKJZlsJiJDp0Dfth5XgOp+YUjrQmFwbrjy1RmotIzXZ3smXBg7TVRB/mIipcKhouCG0kE94gVPPTbZDLh2GdMX3hIgFgJtycAYtpcT/gSNY22b/yBE=",
			wantE: "",
		},
		{
			name: "bad key",
			fields: fields{
				SshKey: "asdgasdgasdg",
			},
			want:  "",
			wantE: "ssh: no key found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &Config{
				SshKey: tt.fields.SshKey,
			}
			got, err := k.SshPubKey()
			if err != nil && err.Error() != tt.wantE {
				t.Errorf("Error = [%v]", err)
			} else if strings.Trim(got, "\n") != tt.want {
				t.Errorf("Parsed key mismatch = [%v], want [%v]", got, tt.want)
			}
		})
	}
}
