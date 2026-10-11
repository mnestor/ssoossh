package config

import (
	"errors"
	"testing"
)

// TestNewConfig_ShouldNameTheSettingWithTheWrongType pins the decode error
// a YAML author sees, in place of mapstructure's Go type names.
func TestNewConfig_ShouldNameTheSettingWithTheWrongType(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "should say a string setting does not take a list",
			config: "key_filename:\n  - a\n  - b\n",
			want:   `invalid configuration: setting "key_filename" takes a string, not a list`,
		},
		{
			name:   "should say a boolean setting does not take arbitrary text",
			config: "use_agent: maybe\n",
			want:   `invalid configuration: setting "use_agent" takes true or false, and maybe is not one`,
		},
		{
			name:   "should say a duration setting takes a duration",
			config: "request_timeout: soon\n",
			want:   `invalid configuration: setting "request_timeout" takes a duration such as 30s or 2m`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newConfigCommand(t, "--config", writeConfig(t, tt.config))

			_, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy)

			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDescribeDecodeError_ShouldPrefixAnUnrecognisedError(t *testing.T) {
	got := describeDecodeError(errors.New("something else"))
	if want := "read configuration: something else"; got.Error() != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJoinInts(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		want string
	}{
		{name: "should list three sizes with or before the last", in: []int{256, 384, 521}, want: "256, 384 or 521"},
		{name: "should list two sizes with or", in: []int{256, 384}, want: "256 or 384"},
		{name: "should print a single size alone", in: []int{256}, want: "256"},
		{name: "should print nothing for no sizes", in: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinInts(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
