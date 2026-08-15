//go:build pam

package main

import (
	"reflect"
	"testing"
	"time"
)

// withDefaults fills in the two fields every parseArgs call sets regardless
// of input (skewTolerance, waitTimeout), so test cases below only need to
// state what's distinctive about them.
func withDefaults(cfg config) config {
	if cfg.skewTolerance == 0 {
		cfg.skewTolerance = defaultSkewTolerance
	}
	if cfg.waitTimeout == 0 {
		cfg.waitTimeout = defaultWaitTimeout
	}
	return cfg
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want config
	}{
		{
			name: "should return defaulted config when args is empty",
			args: nil,
			want: withDefaults(config{}),
		},
		{
			name: "should set server and trusted-ca-file when given key=value pairs",
			args: []string{"server=https://example.com", "trusted-ca-file=/etc/ssoossh/ca.pub"},
			want: withDefaults(config{server: "https://example.com", trustedCAFile: "/etc/ssoossh/ca.pub"}),
		},
		{
			name: "should ignore a token with an empty key",
			args: []string{"=novalue", "server=https://example.com"},
			want: withDefaults(config{server: "https://example.com"}),
		},
		{
			name: "should split only on the first equals so values may contain equals",
			args: []string{"server=https://example.com/a=b"},
			want: withDefaults(config{server: "https://example.com/a=b"}),
		},
		{
			name: "should keep a value's spaces intact, as libpam already merged a bracketed argument into one element",
			args: []string{"trusted-ca-file=a spaced path"},
			want: withDefaults(config{trustedCAFile: "a spaced path"}),
		},
		{
			name: "should treat a bare flag with no equals as boolean true",
			args: []string{"insecure-skip-verify"},
			want: withDefaults(config{insecureSkipVerify: true}),
		},
		{
			name: "should leave insecureSkipVerify false when absent",
			args: []string{"server=https://example.com"},
			want: withDefaults(config{server: "https://example.com", insecureSkipVerify: false}),
		},
		{
			name: "should parse insecure-skip-verify=false as false",
			args: []string{"insecure-skip-verify=false"},
			want: withDefaults(config{insecureSkipVerify: false}),
		},
		{
			name: "should leave insecureSkipVerify false when the value is not a valid bool",
			args: []string{"insecure-skip-verify=maybe"},
			want: withDefaults(config{insecureSkipVerify: false}),
		},
		{
			name: "should leave debug empty when absent",
			args: []string{"server=https://example.com"},
			want: withDefaults(config{server: "https://example.com", debug: ""}),
		},
		{
			name: "should leave debug empty when explicitly false",
			args: []string{"debug=false"},
			want: withDefaults(config{debug: ""}),
		},
		{
			name: "should set debug to stdout when set to stdout",
			args: []string{"debug=stdout"},
			want: withDefaults(config{debug: "stdout"}),
		},
		{
			name: "should set debug to stdout case-insensitively",
			args: []string{"debug=STDOUT"},
			want: withDefaults(config{debug: "stdout"}),
		},
		{
			name: "should set debug to true for a bare debug flag",
			args: []string{"debug"},
			want: withDefaults(config{debug: "true"}),
		},
		{
			name: "should set debug to true for any unrecognized debug value",
			args: []string{"debug=verbose"},
			want: withDefaults(config{debug: "true"}),
		},
		{
			name: "should parse skew-tolerance as a duration",
			args: []string{"skew-tolerance=5s"},
			want: withDefaults(config{skewTolerance: 5 * time.Second}),
		},
		{
			name: "should fall back to the default skew tolerance when unparseable",
			args: []string{"skew-tolerance=not-a-duration"},
			want: withDefaults(config{}),
		},
		{
			name: "should default skew tolerance when absent",
			args: nil,
			want: withDefaults(config{}),
		},
		{
			name: "should parse timeout as a duration",
			args: []string{"timeout=90s"},
			want: withDefaults(config{waitTimeout: 90 * time.Second}),
		},
		{
			name: "should fall back to the default wait timeout when unparseable",
			args: []string{"timeout=not-a-duration"},
			want: withDefaults(config{}),
		},
		{
			name: "should default wait timeout when absent",
			args: nil,
			want: withDefaults(config{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}
