package userdb

import (
	"fmt"
	"sync"
	"testing"
)

func TestContradictionsGroupStored(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		confirmed     bool
		members       []string
		truncated     bool
		changed       bool
		wantHeld      bool
		wantConfirmed bool
	}{
		{name: "should confirm a marked pair when the fresh list still lacks the user", members: []string{"mnestor"}, wantHeld: true, wantConfirmed: true},
		{name: "should confirm a marked pair when the fresh list lacks the user and changed", members: []string{"mnestor"}, changed: true, wantHeld: true, wantConfirmed: true},
		{name: "should clear a marked pair when the fresh list names the user", members: []string{"mnestor", "sonia"}},
		{name: "should clear a marked pair when the fresh list is truncated", members: []string{"mnestor"}, truncated: true},
		{name: "should keep a confirmed pair when the fresh list is unchanged", confirmed: true, members: []string{"mnestor"}, wantHeld: true, wantConfirmed: true},
		{name: "should clear a confirmed pair when the fresh list changed", confirmed: true, members: []string{"mnestor", "erin"}, changed: true},
		{name: "should clear a confirmed pair when the fresh list names the user", confirmed: true, members: []string{"sonia"}, changed: true},
		{name: "should clear a confirmed pair when the fresh list is truncated", confirmed: true, members: []string{"mnestor"}, truncated: true},
		{name: "should confirm a marked pair when the fresh list is empty", members: nil, changed: true, wantHeld: true, wantConfirmed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newContradictions(maxContradictions)
			c.marked("all-users", "sonia")
			if tt.confirmed {
				c.groupStored("all-users", []string{"mnestor"}, false, true)
			}
			c.groupStored("all-users", tt.members, tt.truncated, tt.changed)
			held := c.byGroup["all-users"]["sonia"] != nil
			if held != tt.wantHeld || c.suppressed("all-users", "sonia") != tt.wantConfirmed {
				t.Fatalf("held %v, suppressed %v; want %v, %v", held, c.suppressed("all-users", "sonia"), tt.wantHeld, tt.wantConfirmed)
			}
		})
	}
}

func TestContradictionsShouldOnlySettleTheStoredGroupWhenAnotherGroupIsStored(t *testing.T) {
	t.Parallel()
	c := newContradictions(maxContradictions)
	c.marked("all-users", "sonia")
	c.groupStored("devs", []string{"mnestor"}, false, true)
	if c.suppressed("all-users", "sonia") {
		t.Fatal("storing devs should not confirm an all-users pair")
	}
}

func TestContradictionsShouldAwaitConfirmationAgainWhenAConfirmedPairIsMarked(t *testing.T) {
	t.Parallel()
	c := newContradictions(maxContradictions)
	c.marked("all-users", "sonia")
	c.groupStored("all-users", nil, false, true)
	c.marked("all-users", "sonia")
	if c.suppressed("all-users", "sonia") || c.n != 1 {
		t.Fatalf("suppressed %v with %d pairs, want one pair awaiting confirmation", c.suppressed("all-users", "sonia"), c.n)
	}
}

func TestContradictionsShouldForgetAPairWhenTheUserLeaves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		group     string
		user      string
		wantPairs int
	}{
		{name: "should drop the pair when it is held", group: "all-users", user: "sonia", wantPairs: 1},
		{name: "should keep the others when another user of the group leaves", group: "all-users", user: "erin", wantPairs: 2},
		{name: "should do nothing when the group holds no pairs", group: "devs", user: "sonia", wantPairs: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newContradictions(maxContradictions)
			c.marked("all-users", "sonia")
			c.marked("ops", "sonia")
			c.forget(tt.group, tt.user)
			if c.n != tt.wantPairs {
				t.Fatalf("got %d pairs, want %d", c.n, tt.wantPairs)
			}
		})
	}
	c := newContradictions(maxContradictions)
	c.marked("all-users", "sonia")
	c.forget("all-users", "sonia")
	if len(c.byGroup) != 0 {
		t.Fatalf("got groups %v, want the emptied group dropped", c.byGroup)
	}
}

func TestContradictionsShouldEvictTheOldestPairWhenFull(t *testing.T) {
	t.Parallel()
	c := newContradictions(3)
	for _, u := range []string{"a", "b", "c"} {
		c.marked("all-users", u)
	}
	c.groupStored("all-users", nil, false, true)
	// Marking a again makes b the oldest.
	c.marked("all-users", "a")
	c.marked("ops", "d")
	if c.n != 3 {
		t.Fatalf("got %d pairs, want the cap of 3", c.n)
	}
	for _, tt := range []struct {
		group, user string
		held        bool
	}{
		{"all-users", "a", true}, {"all-users", "b", false}, {"all-users", "c", true}, {"ops", "d", true},
	} {
		if got := c.byGroup[tt.group][tt.user] != nil; got != tt.held {
			t.Errorf("pair %s/%s held %v, want %v", tt.group, tt.user, got, tt.held)
		}
	}
}

func TestContradictionsShouldStayBoundedWhenFlooded(t *testing.T) {
	t.Parallel()
	c := newContradictions(64)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 500 {
				group, user := fmt.Sprintf("g%d", i%7), fmt.Sprintf("u%d-%d", w, i)
				c.marked(group, user)
				c.suppressed(group, user)
				c.groupStored(group, []string{"mnestor"}, false, i%5 == 0)
				if i%3 == 0 {
					c.forget(group, user)
				}
			}
		})
	}
	wg.Wait()
	total := 0
	for _, users := range c.byGroup {
		total += len(users)
	}
	if c.n > 64 || total != c.n {
		t.Fatalf("got %d pairs counted, %d held, want at most 64 and equal", c.n, total)
	}
}
