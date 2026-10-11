package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

// GetCA implements Client.
func (c *HTTPClient) GetCA(ctx context.Context) (string, error) {
	var result apitypes.Envelope[apitypes.CAResponse]
	// Returned unwrapped: the caller names the action ("get CA public
	// key: ..."), and a second layer here read "get CA public key: failed
	// to get CA public key: ...".
	if err := c.doJSON(ctx, http.MethodGet, "/ca", nil, &result); err != nil {
		return "", err
	}
	if err := validateCAKeys(result.Data.CA); err != nil {
		return "", err
	}

	return result.Data.CA, nil
}

// validateCAKeys checks what /api/ca returned: at least one key, one per
// line, each parsing as an authorized_keys public key. Without it an empty
// answer printed a blank line from `ssoossh ca` and exited 0, which an
// operator piping it into TrustedUserCAKeys would not notice until logins
// failed.
func validateCAKeys(ca string) error {
	var found int
	for line := range strings.Lines(ca) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line)); err != nil { //nolint:dogsled // only whether it parses matters here.
			// The line rather than the parse error, which for anything
			// that is not a key is ssh's "no key found; last parsing
			// error for ignored line ..." and names nothing recognisable.
			return fmt.Errorf("the server returned a CA key that is not an SSH public key (%q); the server may be misconfigured", truncateForError(line))
		}
		found++
	}
	if found == 0 {
		return errors.New("the server returned an empty CA key; the server may be misconfigured")
	}
	return nil
}

// truncateForError shortens s to a length an error message can quote.
func truncateForError(s string) string {
	const limit = 40
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
