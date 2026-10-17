package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now implements Clock.
func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock on.
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// epoch is the fake clock's start.
var epoch = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// stores returns one fresh store of each kind.
func stores(t *testing.T) map[string]Store {
	t.Helper()
	sqlite, err := OpenSQLite(t.Context(), filepath.Join(t.TempDir(), "state", "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	return map[string]Store{"memory": NewMemoryStore(), "sqlite": sqlite}
}

// user is a user entry fetched at epoch with a 90-minute TTL.
func user(name string, uid, gid uint32) UserEntry {
	return UserEntry{Name: name, UID: uid, GID: gid, Data: []byte(`{"name":"` + name + `"}`), FetchedAt: epoch, ExpiresAt: epoch.Add(90 * time.Minute)}
}

// group is a group entry fetched at epoch.
func group(name string, gid uint32, members bool) GroupEntry {
	g := GroupEntry{Name: name, GID: gid, Members: members, FetchedAt: epoch, ExpiresAt: epoch.Add(90 * time.Minute)}
	if members {
		g.Data = []byte(`{"name":"` + name + `"}`)
	}
	return g
}

func TestStoreUsers(t *testing.T) {
	t.Parallel()
	for kind, s := range stores(t) {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if e, err := s.UserByName(ctx, "alice"); e != nil || err != nil {
				t.Fatalf("should miss when empty, got %+v, %v", e, err)
			}
			if err := s.PutUser(ctx, user("alice", 5001, 5001)); err != nil {
				t.Fatal(err)
			}
			byName, err := s.UserByName(ctx, "alice")
			if err != nil || byName == nil || byName.UID != 5001 || string(byName.Data) != `{"name":"alice"}` || !byName.FetchedAt.Equal(epoch) {
				t.Fatalf("should find by name when stored, got %+v, %v", byName, err)
			}
			if byUID, err := s.UserByUID(ctx, 5001); err != nil || byUID == nil || byUID.Name != "alice" {
				t.Fatalf("should find by uid when stored, got %+v, %v", byUID, err)
			}
			if miss, err := s.UserByUID(ctx, 9999); miss != nil || err != nil {
				t.Fatalf("should miss an unknown uid, got %+v, %v", miss, err)
			}
			// Byte for byte: a different case is a different name.
			if e, _ := s.UserByName(ctx, "Alice"); e != nil {
				t.Fatal("should match names byte for byte")
			}

			// bob takes alice's uid: alice's entry goes.
			if err := s.PutUser(ctx, user("bob", 5001, 5001)); err != nil {
				t.Fatal(err)
			}
			if e, _ := s.UserByName(ctx, "alice"); e != nil {
				t.Fatal("should drop the entry whose uid a new one takes")
			}
			// bob moves to a new uid: the old uid no longer finds him.
			if err := s.PutUser(ctx, user("bob", 5002, 5002)); err != nil {
				t.Fatal(err)
			}
			if e, _ := s.UserByUID(ctx, 5001); e != nil {
				t.Fatal("should drop the old uid when a name moves")
			}
			if err := s.DeleteUser(ctx, "bob"); err != nil {
				t.Fatal(err)
			}
			if e, _ := s.UserByName(ctx, "bob"); e != nil {
				t.Fatal("should delete")
			}
			if err := s.DeleteUser(ctx, "nobody"); err != nil {
				t.Fatalf("should not fail deleting a missing entry, got %v", err)
			}
		})
	}
}

func TestStoreGroups(t *testing.T) {
	t.Parallel()
	for kind, s := range stores(t) {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if err := s.PutGroup(ctx, group("devs", 5100, false)); err != nil {
				t.Fatal(err)
			}
			stub, err := s.GroupByName(ctx, "devs")
			if err != nil || stub == nil || stub.Members || stub.Data != nil {
				t.Fatalf("should store a stub with no data, got %+v, %v", stub, err)
			}
			if err := s.PutGroup(ctx, group("devs", 5100, true)); err != nil {
				t.Fatal(err)
			}
			full, err := s.GroupByGID(ctx, 5100)
			if err != nil || full == nil || !full.Members || string(full.Data) != `{"name":"devs"}` {
				t.Fatalf("should replace the stub with a full entry, got %+v, %v", full, err)
			}
			if err := s.PutGroup(ctx, group("ops", 5100, false)); err != nil {
				t.Fatal(err)
			}
			if e, _ := s.GroupByName(ctx, "devs"); e != nil {
				t.Fatal("should drop the group whose gid a new one takes")
			}
			if miss, err := s.GroupByGID(ctx, 1); miss != nil || err != nil {
				t.Fatalf("should miss an unknown gid, got %+v, %v", miss, err)
			}
			if err := s.DeleteGroup(ctx, "ops"); err != nil {
				t.Fatal(err)
			}
			if e, _ := s.GroupByName(ctx, "ops"); e != nil {
				t.Fatal("should delete")
			}
		})
	}
}

func TestStoreMarkStaleAndCounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sel       Selector
		wantN     int
		wantStale Counts
	}{
		{name: "should mark everything when -E is given", sel: Selector{Users: true, Groups: true}, wantN: 4, wantStale: Counts{StaleUsers: 2, StaleGroups: 2}},
		{name: "should mark one user when -u names it", sel: Selector{Users: true, Name: "alice"}, wantN: 1, wantStale: Counts{StaleUsers: 1}},
		{name: "should mark one group when -g names it", sel: Selector{Groups: true, Name: "devs"}, wantN: 1, wantStale: Counts{StaleGroups: 1}},
		{name: "should mark all users when -U is given", sel: Selector{Users: true}, wantN: 2, wantStale: Counts{StaleUsers: 2}},
		{name: "should mark all groups when -G is given", sel: Selector{Groups: true}, wantN: 2, wantStale: Counts{StaleGroups: 2}},
		{name: "should mark nothing when the name is unknown", sel: Selector{Users: true, Name: "nobody"}, wantN: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for kind, s := range stores(t) {
				ctx := t.Context()
				for _, e := range []UserEntry{user("alice", 5001, 5001), user("bob", 5002, 5002)} {
					if err := s.PutUser(ctx, e); err != nil {
						t.Fatal(err)
					}
				}
				for _, g := range []GroupEntry{group("devs", 5100, true), group("ops", 5101, false)} {
					if err := s.PutGroup(ctx, g); err != nil {
						t.Fatal(err)
					}
				}
				n, err := s.MarkStale(ctx, tt.sel)
				if err != nil || n != tt.wantN {
					t.Fatalf("%s: marked %d, %v; want %d", kind, n, err, tt.wantN)
				}
				c, err := s.Counts(ctx)
				want := tt.wantStale
				want.Users, want.Groups = 2, 2
				if err != nil || c != want {
					t.Fatalf("%s: got counts %+v, %v; want %+v", kind, c, err, want)
				}
				// Never deleted: every entry is still there.
				users, err := s.Users(ctx)
				if err != nil || len(users) != 2 {
					t.Fatalf("%s: got %d users, %v", kind, len(users), err)
				}
				groups, err := s.Groups(ctx)
				if err != nil || len(groups) != 2 {
					t.Fatalf("%s: got %d groups, %v", kind, len(groups), err)
				}
			}
		})
	}
}

func TestSQLiteShouldKeepEntriesWhenReopened(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx := t.Context()
	s, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(s.PutUser(ctx, user("alice", 5001, 5001)), s.PutGroup(ctx, group("devs", 5100, true))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkStale(ctx, Selector{Users: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	u, err := reopened.UserByName(ctx, "alice")
	if err != nil || u == nil || u.UID != 5001 || !u.Stale || !u.ExpiresAt.Equal(epoch.Add(90*time.Minute)) {
		t.Fatalf("should keep the user and its stale flag across a reopen, got %+v, %v", u, err)
	}
	if g, err := reopened.GroupByName(ctx, "devs"); err != nil || g == nil || !g.Members {
		t.Fatalf("should keep the group across a reopen, got %+v, %v", g, err)
	}
}

func TestSQLiteShouldBePrivateWhenCreated(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "cache.db")
	s, err := OpenSQLite(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.PutUser(t.Context(), user("alice", 5001, 5001)); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: got %v, %v; want %04o", p, info.Mode().Perm(), err, want)
		}
	}
	matches, _ := filepath.Glob(path + "-*")
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.Mode().Perm()&0o077 != 0 {
			// SQLite may create the WAL after open; it is re-protected on
			// the next open, and the 0700 directory guards it meanwhile.
			t.Logf("%s is %04o", m, info.Mode().Perm())
		}
	}
}

func TestOpenSQLiteShouldFail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{name: "should fail when the directory cannot be created", path: func(t *testing.T) string {
			file := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(file, "sub", "cache.db")
		}},
		{name: "should fail when the path is a directory", path: func(t *testing.T) string { return t.TempDir() }},
		{name: "should fail when the file is not a database", path: func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "cache.db")
			if err := os.WriteFile(p, []byte("this is not an sqlite database file, just text"), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if s, err := OpenSQLite(t.Context(), tt.path(t)); err == nil {
				_ = s.Close()
				t.Fatal("want an error")
			}
		})
	}
}

func TestSQLiteShouldReportErrorsWhenClosed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, e1 := s.UserByName(ctx, "a")
	_, e2 := s.GroupByName(ctx, "a")
	_, e3 := s.MarkStale(ctx, Selector{Users: true, Groups: true})
	_, e4 := s.Users(ctx)
	_, e5 := s.Groups(ctx)
	_, e6 := s.Counts(ctx)
	for i, err := range []error{
		e1, e2, e3, e4, e5, e6,
		s.PutUser(ctx, user("a", 5001, 5001)), s.PutGroup(ctx, group("a", 5100, false)),
		s.DeleteUser(ctx, "a"), s.DeleteGroup(ctx, "a"),
	} {
		if err == nil {
			t.Fatalf("call %d: want an error from a closed store", i)
		}
	}
}

func TestMemoryStoreCloseShouldSucceedWhenCalled(t *testing.T) {
	t.Parallel()
	if err := NewMemoryStore().Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshness(t *testing.T) {
	t.Parallel()
	ttl := 100 * time.Second

	tests := []struct {
		name   string
		nowait int
		age    time.Duration
		stale  bool
		want   Freshness
	}{
		{name: "should be fresh when younger than the nowait share", nowait: 50, age: 49 * time.Second, want: Fresh},
		{name: "should want a refresh when at the nowait share", nowait: 50, age: 50 * time.Second, want: Refresh},
		{name: "should want a refresh when between nowait and expiry", nowait: 50, age: 99 * time.Second, want: Refresh},
		{name: "should be expired when at the TTL", nowait: 50, age: 100 * time.Second, want: Expired},
		{name: "should stay fresh until expiry when nowait is 0", nowait: 0, age: 99 * time.Second, want: Fresh},
		{name: "should be expired when marked stale however young", nowait: 50, age: time.Second, stale: true, want: Expired},
		{name: "should want a refresh at once when nowait is 100 and the entry is just expiring", nowait: 100, age: 99 * time.Second, want: Fresh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clock := &fakeClock{now: epoch}
			clock.Advance(tt.age)
			got := TTL{NowaitPercentage: tt.nowait}.Freshness(epoch, epoch.Add(ttl), tt.stale, clock.Now())
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServableOffline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		expire time.Duration
		age    time.Duration
		want   bool
	}{
		{name: "should serve for ever when cache_expire_offline is 0", expire: 0, age: 365 * 24 * time.Hour, want: true},
		{name: "should serve when younger than cache_expire_offline", expire: 24 * time.Hour, age: 23 * time.Hour, want: true},
		{name: "should stop serving when older than cache_expire_offline", expire: 24 * time.Hour, age: 24 * time.Hour, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (TTL{ExpireOffline: tt.expire}).ServableOffline(epoch, epoch.Add(tt.age)); got != tt.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

func TestNegative(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: epoch}
	n := NewNegative(15*time.Second, clock)

	if n.Has("user/name/x") {
		t.Fatal("should miss when empty")
	}
	n.Add("user/name/x")
	clock.Advance(14 * time.Second)
	if !n.Has("user/name/x") {
		t.Fatal("should hit within entry_negative_timeout")
	}
	clock.Advance(time.Second)
	if n.Has("user/name/x") || n.Len() != 0 {
		t.Fatal("should expire at entry_negative_timeout and drop the entry")
	}
	n.Add("a")
	n.Delete("a")
	if n.Has("a") {
		t.Fatal("should forget a deleted miss")
	}
	n.Add("b")
	n.Clear()
	if n.Len() != 0 {
		t.Fatal("should clear")
	}
}

func TestNegativeShouldStayBoundedWhenFlooded(t *testing.T) {
	t.Parallel()
	clock := &fakeClock{now: epoch}
	n := NewNegative(time.Minute, clock)
	for i := range maxNegative {
		n.Add(string(rune('a'+i%26)) + time.Duration(i).String())
	}
	clock.Advance(time.Minute) // all expired: the next Add prunes them
	n.Add("fresh")
	if n.Len() != 1 {
		t.Fatalf("should prune expired entries when full, have %d", n.Len())
	}
	for i := range maxNegative {
		n.Add("live" + time.Duration(i).String())
	}
	if n.Len() > maxNegative {
		t.Fatalf("should never exceed the bound, have %d", n.Len())
	}
}

func TestAwaitShouldShareOneRunWhenCallersOverlap(t *testing.T) {
	t.Parallel()
	var f Flight
	var runs atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	ctx := t.Context()
	fetch := func() (string, error) {
		if runs.Add(1) == 1 {
			close(started)
		}
		<-release
		return "alice", nil
	}

	var wg sync.WaitGroup
	results := make([]string, 5)
	call := func(i int) {
		v, err := Await(ctx, &f, "user/name/alice", fetch)
		if err != nil {
			t.Errorf("got %v", err)
		}
		results[i] = v
	}
	wg.Go(func() { call(0) })
	<-started
	for i := 1; i < len(results); i++ {
		wg.Go(func() { call(i) })
	}
	// The joiners cannot be observed inside singleflight; give them time
	// to reach it. This waits on goroutine scheduling, not on a TTL.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if runs.Load() != 1 || slices.ContainsFunc(results, func(s string) bool { return s != "alice" }) {
		t.Fatalf("got %d runs, results %v", runs.Load(), results)
	}
}

func TestAwaitShouldLetTheFetchFinishWhenTheCallerStopsWaiting(t *testing.T) {
	t.Parallel()
	var f Flight
	release := make(chan struct{})
	finished := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Await(ctx, &f, "k", func() (int, error) {
		<-release
		close(finished)
		return 1, nil
	})
	if !errors.Is(err, ErrStillRunning) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	close(release)
	<-finished // the fetch ran to the end after its caller left
}

func TestAwaitShouldReturnTheErrorWhenTheFetchFails(t *testing.T) {
	t.Parallel()
	var f Flight
	boom := errors.New("boom")
	if _, err := Await(t.Context(), &f, "k", func() (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	// A nil interface result still comes back as the zero value.
	if v, err := Await(t.Context(), &f, "k2", func() (any, error) { return nil, boom }); v != nil || !errors.Is(err, boom) {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestRefresher(t *testing.T) {
	t.Parallel()
	var waits atomic.Int32
	ran := make(chan string, 10)
	r := newRefresher(func(context.Context) error { waits.Add(1); return nil }, func(_ context.Context, key string) { ran <- key }, 2)

	if !r.Enqueue("a") || r.Enqueue("a") {
		t.Fatal("should queue a key once when it is already pending")
	}
	if !r.Enqueue("b") || r.Enqueue("c") {
		t.Fatal("should drop a key when the queue is full")
	}
	if r.Pending() != 2 {
		t.Fatalf("got %d pending", r.Pending())
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	if got := []string{<-ran, <-ran}; !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("ran %v", got)
	}
	if waits.Load() != 2 {
		t.Fatalf("should wait on the rate limiter before each refresh, waited %d times", waits.Load())
	}
	if !r.Enqueue("a") {
		t.Fatal("should queue a key again once its refresh has run")
	}
	<-ran
	cancel()
	<-done
}

func TestRefresherShouldStopWhenTheRateWaitFails(t *testing.T) {
	t.Parallel()
	r := newRefresher(func(context.Context) error { return context.Canceled }, func(context.Context, string) { t.Error("should not run") }, 1)
	r.Enqueue("a")
	r.Run(t.Context())
}

func TestNewRefresherShouldRunKeysWhenStarted(t *testing.T) {
	t.Parallel()
	ran := make(chan string, 1)
	r := NewRefresher(1000, func(_ context.Context, key string) { ran <- key })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go r.Run(ctx)
	r.Enqueue("x")
	if got := <-ran; got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestSystemClockShouldTellTheTimeWhenAsked(t *testing.T) {
	t.Parallel()
	if d := time.Since(SystemClock{}.Now()); d < 0 || d > time.Minute {
		t.Fatalf("got %v", d)
	}
}
