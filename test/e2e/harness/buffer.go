//go:build e2e || resilience || load

package harness

import (
	"bytes"
	"sync"
)

// lockedBuffer is a mutex-guarded byte buffer for capturing a child
// process's output. Every capture in this package is read while the process
// is still running — a readiness poll quoting stderr in its failure message,
// an artifact dump from a cleanup that fires the moment a test fails — and
// os/exec's copier goroutine is writing to it at the same time. A plain
// bytes.Buffer there is a data race, which under -race turns any ordinary
// test failure into a race report on top of it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p, satisfying io.Writer so this can be a cmd.Stdout.
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// WriteLine appends line and a newline, for callers assembling output
// themselves rather than through the copier.
func (b *lockedBuffer) WriteLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(line)
	b.buf.WriteByte('\n')
}

// Bytes returns a copy of everything written so far. The copy matters: the
// caller reads it after the lock is released, while the process writes on.
func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	return string(b.Bytes())
}
