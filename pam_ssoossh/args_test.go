//go:build pam

package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want config
	}{
		{
			name: "should return zero config when args is empty",
			args: nil,
			want: config{},
		},
		{
			name: "should set server and trusted-ca-file when given key=value pairs",
			args: []string{"server=https://example.com", "trusted-ca-file=/etc/ssoossh/ca.pub"},
			want: config{server: "https://example.com", trustedCAFile: "/etc/ssoossh/ca.pub"},
		},
		{
			name: "should ignore a token with an empty key",
			args: []string{"=novalue", "server=https://example.com"},
			want: config{server: "https://example.com"},
		},
		{
			name: "should split only on the first equals so values may contain equals",
			args: []string{"server=https://example.com/a=b"},
			want: config{server: "https://example.com/a=b"},
		},
		{
			name: "should unquote a double-quoted value",
			args: []string{`server="https://example.com"`},
			want: config{server: "https://example.com"},
		},
		{
			name: "should unquote a single-quoted value",
			args: []string{`server='https://example.com'`},
			want: config{server: "https://example.com"},
		},
		{
			name: "should fall back to trimming quotes when the value is not valid Go-quoted syntax",
			args: []string{`server="a\qb"`},
			want: config{server: `a\qb`},
		},
		{
			name: "should treat a bare flag with no equals as boolean true",
			args: []string{"insecure-skip-verify"},
			want: config{insecureSkipVerify: true},
		},
		{
			name: "should leave insecureSkipVerify false when absent",
			args: []string{"server=https://example.com"},
			want: config{server: "https://example.com", insecureSkipVerify: false},
		},
		{
			name: "should parse insecure-skip-verify=false as false",
			args: []string{"insecure-skip-verify=false"},
			want: config{insecureSkipVerify: false},
		},
		{
			name: "should leave insecureSkipVerify false when the value is not a valid bool",
			args: []string{"insecure-skip-verify=maybe"},
			want: config{insecureSkipVerify: false},
		},
		{
			name: "should leave debug empty when absent",
			args: []string{"server=https://example.com"},
			want: config{server: "https://example.com", debug: ""},
		},
		{
			name: "should leave debug empty when explicitly false",
			args: []string{"debug=false"},
			want: config{debug: ""},
		},
		{
			name: "should set debug to stdout when set to stdout",
			args: []string{"debug=stdout"},
			want: config{debug: "stdout"},
		},
		{
			name: "should set debug to stdout case-insensitively",
			args: []string{"debug=STDOUT"},
			want: config{debug: "stdout"},
		},
		{
			name: "should set debug to true for a bare debug flag",
			args: []string{"debug"},
			want: config{debug: "true"},
		},
		{
			name: "should set debug to true for any unrecognized debug value",
			args: []string{"debug=verbose"},
			want: config{debug: "true"},
		},
		{
			name: "should reassemble a quoted value split across multiple tokens",
			args: []string{`trusted-ca-file="a`, `spaced`, `path"`},
			want: config{trustedCAFile: "a spaced path"},
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

func TestRegroupQuotedArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "should return empty slice when args is empty",
			args: []string{},
			want: []string{},
		},
		{
			name: "should leave unquoted tokens unchanged",
			args: []string{"server=https://example.com", "debug"},
			want: []string{"server=https://example.com", "debug"},
		},
		{
			name: "should leave a token whose quotes are already closed unchanged",
			args: []string{`key="value"`},
			want: []string{`key="value"`},
		},
		{
			name: "should join tokens split inside a double-quoted value",
			args: []string{`key="a`, `spaced`, `value"`},
			want: []string{`key="a spaced value"`},
		},
		{
			name: "should join tokens split inside a single-quoted value",
			args: []string{`key='a`, `spaced`, `value'`},
			want: []string{`key='a spaced value'`},
		},
		{
			name: "should absorb the remaining tokens when a quote is never closed",
			args: []string{`key="a`, `spaced`, `value`},
			want: []string{`key="a spaced value`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := regroupQuotedArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("regroupQuotedArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestOpeningQuote(t *testing.T) {
	tests := []struct {
		name string
		tok  string
		want byte
	}{
		{
			name: "should return 0 when the token has no equals",
			tok:  "debug",
			want: 0,
		},
		{
			name: "should return 0 when the value is empty",
			tok:  "key=",
			want: 0,
		},
		{
			name: "should return 0 when the value does not start with a quote",
			tok:  "key=value",
			want: 0,
		},
		{
			name: "should return the double quote when the value opens one and does not close it",
			tok:  `key="value`,
			want: '"',
		},
		{
			name: "should return the single quote when the value opens one and does not close it",
			tok:  `key='value`,
			want: '\'',
		},
		{
			name: "should return 0 when the double-quoted value is already closed",
			tok:  `key="value"`,
			want: 0,
		},
		{
			name: "should return the quote when the value is a single unmatched quote character",
			tok:  `key="`,
			want: '"',
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openingQuote(tt.tok)
			if got != tt.want {
				t.Errorf("openingQuote(%q) = %q, want %q", tt.tok, got, tt.want)
			}
		})
	}
}
