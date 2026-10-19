package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/upstream"
	"github.com/mnestor/ssoossh/nssd/userdb"
)

func TestPrintGroupFindings(t *testing.T) {
	t.Parallel()
	seen := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	local := uint32(100)

	tests := []struct {
		name     string
		findings policy.Findings
		want     string
	}{
		{name: "should print nothing when no group was refused", want: ""},
		{name: "should list a collision and a refusal when one of each was met",
			findings: policy.Findings{
				Collisions: []policy.NameCollision{{Name: "users", GID: 2005, LocalGID: &local, FirstSeen: seen}},
				Refused:    []policy.RefusedGroup{{Name: "oldstaff", GID: 500, Reason: "gid 500 is outside min_id..max_id (1000..unlimited)", FirstSeen: seen}},
			},
			want: "collisions:    1 group name collision (a directory group with a local group's name; not resolved here, rename one of the two)\n" +
				"  users: directory gid 2005, local gid 100, first seen 2026-10-18T09:00:00Z\n" +
				"refused:       1 refused group (not served on this host)\n" +
				"  oldstaff: gid 500 is outside min_id..max_id (1000..unlimited), first seen 2026-10-18T09:00:00Z\n"},
		{name: "should count the rest when the lists are capped",
			findings: policy.Findings{
				Collisions:     []policy.NameCollision{{Name: "nisgrp", GID: 2006, FirstSeen: seen}},
				MoreCollisions: 3,
				Refused:        []policy.RefusedGroup{{Name: "a", GID: 1, Reason: "r", FirstSeen: seen}},
				MoreRefused:    1,
			},
			want: "collisions:    4 group name collisions (a directory group with a local group's name; not resolved here, rename one of the two)\n" +
				"  nisgrp: directory gid 2006, local gid unknown, first seen 2026-10-18T09:00:00Z\n" +
				"  and 3 more\n" +
				"refused:       2 refused groups (not served on this host)\n" +
				"  a: r, first seen 2026-10-18T09:00:00Z\n" +
				"  and 1 more\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			printGroupFindings(&out, tt.findings)
			if out.String() != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", out.String(), tt.want)
			}
		})
	}
}

func TestDescribeTrust(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caFile string
		pins   int
		want   string
	}{
		{name: "should name the system store when tls_ca_file is system", caFile: "system", want: "system store (any CA this host trusts)"},
		{name: "should name the CA file when tls_ca_file is a path", caFile: "/etc/ssoossh/isrg-roots.pem", want: "CA file /etc/ssoossh/isrg-roots.pem"},
		{name: "should count the pins when only pins are set", pins: 2, want: "key pin (2 pins)"},
		{name: "should say pin when there is one", pins: 1, want: "key pin (1 pin)"},
		{name: "should name both when a CA file and a pin are set", caFile: "/etc/ssoossh/ca.pem", pins: 1, want: "CA file /etc/ssoossh/ca.pem and key pin (1 pin)"},
		{name: "should name both when the system store and pins are set", caFile: "system", pins: 2, want: "system store (any CA this host trusts) and key pin (2 pins)"},
		{name: "should print a dash when nothing is set", want: "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := describeTrust(tt.caFile, tt.pins); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintDaemonStatusShouldNameTheTrustWhenTheDaemonReportsIt(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	printDaemonStatus(&out, &userdb.Status{Upstream: upstream.Status{Server: "https://sso.example.com", TLSCAFile: "system"}})
	if !strings.Contains(out.String(), "server:        https://sso.example.com\ntrust:         system store (any CA this host trusts)\n") {
		t.Fatalf("got:\n%s", out.String())
	}
}

func TestPrintDaemonStatusShouldListGroupFindingsWhenTheDaemonReportsThem(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	printDaemonStatus(&out, &userdb.Status{Groups: policy.Findings{Collisions: []policy.NameCollision{{Name: "users", GID: 2005}}}})
	if !strings.Contains(out.String(), "overrides:     0\ncollisions:    1 group name collision") {
		t.Fatalf("got:\n%s", out.String())
	}
}
