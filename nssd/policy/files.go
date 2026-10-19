package policy

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/mnestor/ssoossh/nssd/config"
)

// watched holds a file's parsed contents and re-reads it when the file's
// stamp changes, so an edit to /etc/passwd takes effect on the next lookup
// without a restart. Each lookup costs one stat.
type watched[T any] struct {
	path  string
	read  func(path string) (T, error)
	mu    sync.Mutex
	stamp config.Stamp
	value T
	err   error
	ready bool
}

// newWatched returns a watched file read by read on first use.
func newWatched[T any](path string, read func(string) (T, error)) *watched[T] {
	return &watched[T]{path: path, read: read}
}

// get returns the current contents, re-reading the file when it changed.
// A read that fails is remembered until the file changes again, so a
// broken file costs a stat per lookup, not a parse.
func (w *watched[T]) get() (T, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	stamp, err := config.StampOf(w.path)
	if err != nil {
		var zero T
		return zero, err
	}
	if !w.ready || stamp != w.stamp {
		w.value, w.err = w.read(w.path)
		w.stamp, w.ready = stamp, true
	}
	return w.value, w.err
}

// invalidate makes the next get re-read the file whatever its stamp.
func (w *watched[T]) invalidate() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ready = false
}

// localDB is one of /etc/passwd or /etc/group: its names and its ids,
// each mapped to the first entry that lists it.
type localDB struct {
	// names maps a name to its id.
	names map[string]localID
	// ids maps an id to the first name listed with it.
	ids map[uint32]string
}

// localID is a local name's id; known is false when its line gives none
// (a compat "+name" entry).
type localID struct {
	id    uint32
	known bool
}

// hasName reports whether name is listed.
func (db localDB) hasName(name string) bool {
	_, ok := db.names[name]
	return ok
}

// hasID reports whether id is listed.
func (db localDB) hasID(id uint32) bool {
	_, ok := db.ids[id]
	return ok
}

// idOf returns the id listed for name, and whether name is listed at all.
func (db localDB) idOf(name string) (localID, bool) {
	id, ok := db.names[name]
	return id, ok
}

// nameOf returns the first name listed with id.
func (db localDB) nameOf(id uint32) (string, bool) {
	name, ok := db.ids[id]
	return name, ok
}

// readLocalDB reads a passwd- or group-format file: name in the first
// field, the id in the third. A missing file is an empty database (a
// container may have no /etc/group); any other read error fails closed in
// the caller.
func readLocalDB(path string) (localDB, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return localDB{names: map[string]localID{}, ids: map[uint32]string{}}, nil
	}
	if err != nil {
		return localDB{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parseLocalDB(data), nil
}

// parseLocalDB parses passwd or group lines. Comments and blank lines are
// skipped. A compat-mode "+name" or "-name" entry counts as the name
// itself, which errs towards treating more names as local, the safe side.
// A line with no parsable id still records its name. The first line that
// lists a name or an id wins, as getgrnam and getgrgid return the first.
func parseLocalDB(data []byte) localDB {
	db := localDB{names: map[string]localID{}, ids: map[uint32]string{}}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		name := strings.TrimLeft(fields[0], "+-")
		var id localID
		if len(fields) > 2 {
			if n, err := strconv.ParseUint(fields[2], 10, 32); err == nil {
				id = localID{id: uint32(n), known: true}
				if _, seen := db.ids[id.id]; !seen {
					db.ids[id.id] = name
				}
			}
		}
		if _, seen := db.names[name]; name != "" && !seen {
			db.names[name] = id
		}
	}
	return db
}

// readShells reads /etc/shells: one absolute path per line, comments and
// blank lines skipped. A missing file lists no shells.
func readShells(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var shells []string
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			shells = append(shells, line)
		}
	}
	return shells, nil
}
