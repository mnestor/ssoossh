package policy

import (
	"strconv"
	"testing"
)

func TestFindingsShouldStopTrackingWhenTheBoundIsReached(t *testing.T) {
	t.Parallel()
	f := newFindings()
	for i := range findingsTracked {
		if !f.refused(RefusedGroup{Name: "g" + strconv.Itoa(i), GID: uint32(i)}) {
			t.Fatalf("entry %d should be new", i)
		}
	}
	if f.refused(RefusedGroup{Name: "one-more", GID: 1}) {
		t.Fatal("should not track a new key past findingsTracked")
	}
	if got := f.snapshot(); len(got.Refused)+got.MoreRefused != findingsTracked {
		t.Fatalf("got %d listed and %d more, want %d in all", len(got.Refused), got.MoreRefused, findingsTracked)
	}
}

func TestFindingsSnapshotShouldNotShareTheListWhenTheRecordGrows(t *testing.T) {
	t.Parallel()
	f := newFindings()
	f.collision(NameCollision{Name: "a", GID: 1})
	snap := f.snapshot()
	f.collision(NameCollision{Name: "b", GID: 2})
	if len(snap.Collisions) != 1 {
		t.Fatalf("got %+v, want the snapshot unchanged", snap)
	}
}
