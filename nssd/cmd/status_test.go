package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/nssd/policy"
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

func TestPrintDaemonStatusShouldListGroupFindingsWhenTheDaemonReportsThem(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	printDaemonStatus(&out, &userdb.Status{Groups: policy.Findings{Collisions: []policy.NameCollision{{Name: "users", GID: 2005}}}})
	if !strings.Contains(out.String(), "overrides:     0\ncollisions:    1 group name collision") {
		t.Fatalf("got:\n%s", out.String())
	}
}
