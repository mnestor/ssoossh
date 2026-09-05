package principalsmap

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Format renders m in the subset parse accepts, so what this package writes
// it can always read back. Accounts are sorted by name, which makes the
// output of two runs over the same mapping byte-identical and keeps a
// hand-edited file's diff to the line that actually changed; each account's
// principals stay in the order they were added.
//
// A value that cannot survive a round trip is an error rather than
// something written and mangled: an account or principal carrying a
// newline, a backslash, or both kinds of quote has no spelling in the
// subset unquote accepts.
func Format(m PrincipalsMap) ([]byte, error) {
	accounts := make([]string, 0, len(m))
	for account := range m {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	var b bytes.Buffer
	for _, account := range accounts {
		key, err := quoteFor(account)
		if err != nil {
			return nil, fmt.Errorf("account %q: %w", account, err)
		}
		// An account with no principals is written as a bare "account:" --
		// the same thing parse reads as an account whose list never
		// arrived, and what PrincipalsMap.Allowed treats as "nobody".
		fmt.Fprintf(&b, "%s:\n", key)
		for _, principal := range m[account] {
			value, err := quoteFor(principal)
			if err != nil {
				return nil, fmt.Errorf("account %q, principal %q: %w", account, principal, err)
			}
			fmt.Fprintf(&b, "  - %s\n", value)
		}
	}
	return b.Bytes(), nil
}

// WriteFile writes m to path.
//
// The file is opened and overwritten in place rather than written to a temp
// file and renamed. A rename replaces the directory entry, so the file that
// lands carries the writing process's ownership and a fresh mode, silently
// discarding whatever an operator set on the file it replaced. This one is
// read by sshd as AuthorizedPrincipalsCommandUser -- an unprivileged
// account that is not the root writing it -- so a mode or group reset is
// exactly what stops every principal lookup from answering.
//
// The cost is that the write is not atomic. O_TRUNC is deliberately not set
// so the file is never momentarily empty, and the truncate below runs after
// the content is down, but a reader arriving in between can see the new
// content followed by a tail of the old. That window is microseconds inside
// an interactive administrative command; the alternative loses the file's
// permissions on every write.
func WriteFile(path string, m PrincipalsMap) error {
	data, err := Format(m)
	if err != nil {
		return err
	}

	// 0644 on the create path, and only there: this file is read by an
	// account other than the root that writes it, so a fresh one has to be
	// readable or the first lookup after setup answers nothing. It holds no
	// secret -- it says which principals may assume which local account,
	// the same class of statement as an authorized_keys file. An operator
	// who would rather it were not world-readable sets the mode once and
	// this function preserves it from then on, which is what opening the
	// file in place buys.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open principals map for writing: %w", err)
	}

	if err := overwrite(f, data); err != nil {
		f.Close() // the write error is the one worth reporting.
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// overwrite writes data from the start of f and drops whatever the previous
// contents left beyond it, then flushes. Split out so WriteFile's error
// paths stay one deep, and so the truncate is impossible to reorder ahead
// of the write by accident.
func overwrite(f *os.File, data []byte) error {
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(n)); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	// The file is read on every login attempt by a different process, so
	// the content has to be durable before this command reports success.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	return nil
}

// quoteFor returns value spelled so parse reads it back unchanged, wrapping
// it in quotes only when leaving it bare would change its meaning.
func quoteFor(value string) (string, error) {
	if strings.ContainsAny(value, "\n\r") {
		return "", fmt.Errorf("contains a line break, which this format cannot express")
	}
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("contains a backslash, which this format does not escape")
	}
	if !needsQuoting(value) {
		return value, nil
	}

	// Wrap in whichever quote the value does not itself contain. Carrying
	// both leaves nothing to wrap it in, because escapes are not
	// interpreted on the way back in.
	switch {
	case !strings.Contains(value, `"`):
		return `"` + value + `"`, nil
	case !strings.Contains(value, `'`):
		return `'` + value + `'`, nil
	default:
		return "", fmt.Errorf("contains both kinds of quote, which this format cannot express")
	}
}

// needsQuoting reports whether value read back bare would be something
// other than itself: empty, one of YAML's spellings of null, or carrying a
// character the line-oriented parse gives its own meaning to.
//
//   - a colon ends a key, so an account holding one would be cut short
//   - a "#" after whitespace starts a comment, truncating the value
//   - a leading "-" is how a list item is recognized, ahead of any
//     "account:" line, so an account named that way would be read as an
//     item of whatever came before
//   - a quote anywhere, not just leading: splitKey and stripComment track
//     quoting as they scan a line, so a lone quote inside an account name
//     hides the colon that ends the key and the line stops parsing as an
//     account at all
//   - surrounding whitespace is trimmed, so it would not survive
func needsQuoting(value string) bool {
	if value == "" || value == "null" || value == "~" {
		return true
	}
	if strings.ContainsAny(value, `:#"'`) {
		return true
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
		return true
	}
	return strings.TrimSpace(value) != value
}
