package bootstrap

// This is taken from https://github.com/pocket-id/pocket-id/blob/main/backend/internal/bootstrap/db_bootstrap.go

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	sqlitelib "github.com/glebarez/go-sqlite"
	"golang.org/x/text/unicode/norm"
)

// parseSqliteConnectionString normalizes connString into a "file:" URL with
// the default and required SQLite parameters applied, and reports the
// absolute path to the database file and whether it's an in-memory database.
func parseSqliteConnectionString(connString string) (parsedConnString string, dbPath string, isMemoryDB bool, err error) {
	if !strings.HasPrefix(connString, "file:") {
		connString = "file:" + connString
	}

	// Check if we're using an in-memory database
	isMemoryDB = isSqliteInMemory(connString)

	// Parse the connection string
	connStringURL, err := url.Parse(connString)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to parse SQLite connection string: %w", err)
	}

	// Convert options for the old SQLite driver to the new one
	convertSqlitePragmaArgs(connStringURL)

	// Add the default and required params
	err = addSqliteDefaultParameters(connStringURL, isMemoryDB)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid SQLite connection string: %w", err)
	}

	// Get the absolute path to the database
	// Here, we know for a fact that the ? is present
	parsedConnString = connStringURL.String()
	idx := strings.IndexRune(parsedConnString, '?')
	dbPath, err = filepath.Abs(parsedConnString[len("file:"):idx])
	if err != nil {
		return "", "", false, fmt.Errorf("failed to determine absolute path to the database: %w", err)
	}

	return parsedConnString, dbPath, isMemoryDB, nil
}

// The official C implementation of SQLite allows some additional properties in the connection string
// that are not supported in the in the modernc.org/sqlite driver, and which must be passed as PRAGMA args instead.
// To ensure that people can use similar args as in the C driver, which was also used by Pocket ID
// previously (via github.com/mattn/go-sqlite3), we are converting some options.
// Note this function updates connStringUrl.
func convertSqlitePragmaArgs(connStringURL *url.URL) {
	// Reference: https://github.com/mattn/go-sqlite3?tab=readme-ov-file#connection-string
	// This only includes a subset of options, excluding those that are not relevant to us
	qs := make(url.Values, len(connStringURL.Query()))
	for k, v := range connStringURL.Query() {
		switch strings.ToLower(k) {
		case "_auto_vacuum", "_vacuum":
			qs.Add("_pragma", "auto_vacuum("+v[0]+")")
		case "_busy_timeout", "_timeout":
			qs.Add("_pragma", "busy_timeout("+v[0]+")")
		case "_case_sensitive_like", "_cslike":
			qs.Add("_pragma", "case_sensitive_like("+v[0]+")")
		case "_foreign_keys", "_fk":
			qs.Add("_pragma", "foreign_keys("+v[0]+")")
		case "_locking_mode", "_locking":
			qs.Add("_pragma", "locking_mode("+v[0]+")")
		case "_secure_delete":
			qs.Add("_pragma", "secure_delete("+v[0]+")")
		case "_synchronous", "_sync":
			qs.Add("_pragma", "synchronous("+v[0]+")")
		default:
			// Pass other query-string args as-is
			qs[k] = v
		}
	}

	// Update the connStringUrl object
	connStringURL.RawQuery = qs.Encode()
}

// addSqliteDefaultParameters applies sensible defaults to the SQLite connection string:
// - busy_timeout for lock contention (2.5s)
// - journal_mode based on database type (WAL for read-write, DELETE for read-only, MEMORY for in-memory)
// - foreign_keys enabled
// - _txlock set to "immediate" for transactional correctness
// Note this function updates connStringURL.
func addSqliteDefaultParameters(connStringURL *url.URL, isMemoryDB bool) error {
	const defaultBusyTimeout = 2500 * time.Millisecond

	qs := connStringURL.Query()
	if len(qs) == 0 {
		qs = make(url.Values, 2)
	}

	isReadOnly := detectReadOnlyMode(qs)
	normalizeMode(qs)
	normalizeTxLock(qs)

	var hasBusyTimeout, hasJournalMode bool
	if len(qs["_pragma"]) == 0 {
		qs["_pragma"] = make([]string, 0, 3)
	} else {
		for _, p := range qs["_pragma"] {
			p = strings.ToLower(p)
			switch {
			case strings.HasPrefix(p, "busy_timeout"):
				hasBusyTimeout = true
			case strings.HasPrefix(p, "journal_mode"):
				hasJournalMode = true
			case strings.HasPrefix(p, "foreign_keys"):
				return errors.New("found forbidden option '_pragma=foreign_keys' in the connection string")
			}
		}
	}

	if !hasBusyTimeout {
		qs["_pragma"] = append(qs["_pragma"], fmt.Sprintf("busy_timeout(%d)", defaultBusyTimeout.Milliseconds()))
	}
	if !hasJournalMode {
		qs["_pragma"] = append(qs["_pragma"], journalModeForDB(isMemoryDB, isReadOnly))
	}

	qs["_pragma"] = append(qs["_pragma"], "foreign_keys(1)")
	connStringURL.RawQuery = qs.Encode()

	return nil
}

// detectReadOnlyMode checks if the connection string specifies a read-only or immutable database.
func detectReadOnlyMode(qs url.Values) bool {
	if len(qs["mode"]) > 0 && strings.ToLower(qs["mode"][0]) == "ro" {
		return true
	}
	if len(qs["immutable"]) > 0 && strings.ToLower(qs["immutable"][0]) == "1" {
		return true
	}
	return false
}

// normalizeMode normalizes the mode query parameter to lowercase.
func normalizeMode(qs url.Values) {
	if len(qs["mode"]) > 0 {
		qs["mode"] = []string{strings.ToLower(qs["mode"][0])}
	}
}

// normalizeTxLock normalizes the _txlock query parameter and validates it.
func normalizeTxLock(qs url.Values) {
	if len(qs["_txlock"]) > 0 {
		qs["_txlock"] = []string{strings.ToLower(qs["_txlock"][0])}
		if qs["_txlock"][0] != "immediate" {
			slog.Warn("SQLite connection is being created with a _txlock different from the recommended value 'immediate'")
		}
	} else {
		qs["_txlock"] = []string{"immediate"}
	}
}

// journalModeForDB returns the appropriate journal_mode pragma for the database type.
func journalModeForDB(isMemoryDB, isReadOnly bool) string {
	switch {
	case isMemoryDB:
		return "journal_mode(MEMORY)"
	case isReadOnly:
		return "journal_mode(DELETE)"
	default:
		return "journal_mode(WAL)"
	}
}

// isSqliteInMemory returns true if the connection string is for an in-memory database.
func isSqliteInMemory(connString string) bool {
	lc := strings.ToLower(connString)

	// First way to define an in-memory database is to use ":memory:" or "file::memory:" as connection string
	if strings.HasPrefix(lc, ":memory:") || strings.HasPrefix(lc, "file::memory:") {
		return true
	}

	// Another way is to pass "mode=memory" in the "query string"
	idx := strings.IndexRune(lc, '?')
	if idx < 0 {
		return false
	}
	qs, err := url.ParseQuery(lc[(idx + 1):])
	if err != nil {
		return false
	}

	return len(qs["mode"]) > 0 && qs["mode"][0] == "memory"
}

// ensureSqliteDatabaseDir creates the parent directory for the SQLite database file if it doesn't exist yet.
func ensureSqliteDatabaseDir(dbPath string) error {
	dir := filepath.Dir(dbPath)

	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("SQLite database directory '%s' is not a directory", dir)
		}
		return nil
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create SQLite database directory '%s': %w", dir, err)
		}
		return nil
	default:
		return fmt.Errorf("failed to check SQLite database directory '%s': %w", dir, err)
	}
}

// ensureSqliteTempDir ensures that SQLite has a directory where it can write temporary files if needed
// The default directory may not be writable when using a container with a read-only root file system
// See: https://www.sqlite.org/tempfiles.html
func ensureSqliteTempDir(dbPath string) error {
	// Per docs, SQLite tries these folders in order (excluding those that aren't applicable to us):
	//
	// - The SQLITE_TMPDIR environment variable
	// - The TMPDIR environment variable
	// - /var/tmp
	// - /usr/tmp
	// - /tmp
	//
	// Source: https://www.sqlite.org/tempfiles.html#temporary_file_storage_locations
	//
	// First, let's check if SQLITE_TMPDIR or TMPDIR are set, in which case we trust the user has taken care of the problem already
	if os.Getenv("SQLITE_TMPDIR") != "" || os.Getenv("TMPDIR") != "" {
		return nil
	}

	// Now, let's check if /var/tmp, /usr/tmp, or /tmp exist and are writable
	for _, dir := range []string{"/var/tmp", "/usr/tmp", "/tmp"} {
		ok, err := IsWritableDir(dir)
		if err != nil {
			return fmt.Errorf("failed to check if %s is writable: %w", dir, err)
		}
		if ok {
			// We found a folder that's writable
			return nil
		}
	}

	// If we're here, there's no temporary directory that's writable (not unusual for
	// containers with a read-only root file system), so we set SQLITE_TMPDIR to the
	// folder where the SQLite database is set
	err := os.Setenv("SQLITE_TMPDIR", dbPath)
	if err != nil {
		return fmt.Errorf("failed to set SQLITE_TMPDIR environmental variable: %w", err)
	}

	slog.Debug("Set SQLITE_TMPDIR to the database directory", "path", dbPath)

	return nil
}

// IsWritableDir checks if a directory exists and is writable.
func IsWritableDir(dir string) (bool, error) {
	// Check if directory exists and it's actually a directory
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to stat '%s': %w", dir, err)
	}
	if !info.IsDir() {
		return false, nil
	}

	// Generate a random suffix for the test file to avoid conflicts
	randomBytes := make([]byte, 8)
	_, err = io.ReadFull(rand.Reader, randomBytes)
	if err != nil {
		return false, fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Check if directory is writable by trying to create a temporary file
	testFile := filepath.Join(dir, ".pocketid_test_write_"+hex.EncodeToString(randomBytes))
	defer os.Remove(testFile)

	file, err := os.Create(testFile)
	if err != nil {
		if os.IsPermission(err) || errors.Is(err, syscall.EROFS) {
			return false, nil
		}

		return false, fmt.Errorf("failed to create test file: %w", err)
	}

	_ = file.Close()

	return true, nil
}

// registerSqliteFunctionsOnce guards registerSqliteFunctions so that
// connecting to SQLite more than once per process (e.g. across tests) does
// not panic on a duplicate function registration.
var registerSqliteFunctionsOnce sync.Once

// registerSqliteFunctions registers custom scalar functions used by the
// embedded SQLite migrations, such as normalize(text, form) for Unicode
// normalization.
func registerSqliteFunctions() {
	registerSqliteFunctionsOnce.Do(registerSqliteFunctionsUnsafe)
}

// normalizationForms maps the `normalize(text, form)` SQL function's form
// argument (case-insensitive) to its unicode/norm constant.
var normalizationForms = map[string]norm.Form{
	"nfc":  norm.NFC,
	"nfd":  norm.NFD,
	"nfkc": norm.NFKC,
	"nfkd": norm.NFKD,
}

// registerSqliteFunctionsUnsafe performs the actual registration; call it
// only through registerSqliteFunctions.
func registerSqliteFunctionsUnsafe() {
	// Register the `normalize(text, form)` function, which performs Unicode normalization on the text
	// This is currently only used in migration functions
	sqlitelib.MustRegisterDeterministicScalarFunction("normalize", 2, func(ctx *sqlitelib.FunctionContext, args []driver.Value) (driver.Value, error) {
		if len(args) != 2 {
			return nil, errors.New("normalize requires 2 arguments")
		}

		arg0, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("first argument for normalize is not a string: %T", args[0])
		}

		arg1, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("second argument for normalize is not a string: %T", args[1])
		}

		form, ok := normalizationForms[strings.ToLower(arg1)]
		if !ok {
			return nil, fmt.Errorf("unsupported form: %s", arg1)
		}

		if len(arg0) == 0 {
			return arg0, nil
		}

		return form.String(arg0), nil
	})
}
