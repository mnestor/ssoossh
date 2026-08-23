package config

import (
	"strings"
	"testing"
)

// ConfigSource.String is what --debug prints for each entry in the merge
// chain, and it was the only rendering in this package at 0% coverage. The
// error case is the one that matters: it is the whole reason the type
// exists, because viper skips a config file it cannot parse and nothing
// else ever says so.
func TestConfigSource_ShouldRenderEachOutcome(t *testing.T) {
	tests := []struct {
		name   string
		source ConfigSource
		want   []string
		absent []string
	}{
		{
			name:   "merged file names its path",
			source: ConfigSource{Label: "user file", Path: "/home/u/.config/ssoossh.yaml", Status: SourceMerged},
			want:   []string{"user file", "/home/u/.config/ssoossh.yaml", "merged"},
		},
		{
			name:   "absent file still names its path",
			source: ConfigSource{Label: "system file", Path: "/etc/ssoossh/ssoossh.yaml", Status: SourceAbsent},
			want:   []string{"system file", "/etc/ssoossh/ssoossh.yaml", "absent"},
		},
		{
			name:   "error carries the parse failure",
			source: ConfigSource{Label: "local file", Path: "./ssoossh.yaml", Status: SourceError, Err: "yaml: line 1: bad"},
			want:   []string{"local file", "./ssoossh.yaml", "error", "yaml: line 1: bad"},
		},
		{
			name:   "a source with no file omits the parentheses",
			source: ConfigSource{Label: "command-line flags", Status: SourceNotGiven},
			want:   []string{"command-line flags", "not given"},
			absent: []string{"("},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.source.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("got %q, want it to contain %q", got, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Errorf("got %q, want it not to contain %q", got, absent)
				}
			}
		})
	}
}

// describeFailure supplies the reason in the error for a --config file that
// could not be used, so the two failure modes stay distinguishable: a typo
// in the path reads differently from a file that is there and broken.
func TestConfigSource_ShouldDescribeWhyItDidNotContribute(t *testing.T) {
	tests := []struct {
		name   string
		source ConfigSource
		want   string
	}{
		{name: "absent", source: ConfigSource{Status: SourceAbsent}, want: "no such file"},
		{name: "error", source: ConfigSource{Status: SourceError, Err: "yaml: line 1: bad"}, want: "yaml: line 1: bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.source.describeFailure(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
