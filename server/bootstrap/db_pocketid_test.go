package bootstrap

// Test methodology: table-driven tests with t.Parallel() for parallelization.
// Each test verifies one specific SQLite connection string parsing behavior.
// Tests use t.TempDir() for temporary database files.

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

func TestIsSqliteInMemory_ShouldDetectMemoryColonPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"should detect :memory: prefix", ":memory:", true},
		{"should detect file::memory: prefix", "file::memory:", true},
		{"should detect uppercase FILE::MEMORY: prefix", "FILE::MEMORY:", true},
		{"should detect mode=memory query param", "file:test.db?mode=memory", true},
		{"should detect mode=memory among other params", "file:test.db?cache=shared&mode=memory", true},
		{"should not detect plain file path", "file:/tmp/test.db", false},
		{"should not detect file path without query string", "test.db", false},
		{"should not detect mode=rwc", "file:test.db?mode=rwc", false},
		{"should not detect empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isSqliteInMemory(tt.input); got != tt.want {
				t.Errorf("isSqliteInMemory(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSqliteConnectionString_ShouldAddFilePrefixWhenMissing(t *testing.T) {
	t.Parallel()

	parsed, _, _, err := parseSqliteConnectionString("test.db")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.HasPrefix(parsed, "file:") {
		t.Errorf("expected parsed connection string to start with 'file:', got %q", parsed)
	}
}

func TestParseSqliteConnectionString_ShouldNotDoublePrefixFile(t *testing.T) {
	t.Parallel()

	parsed, _, _, err := parseSqliteConnectionString("file:test.db")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if strings.HasPrefix(parsed, "file:file:") {
		t.Errorf("expected connection string to not double the file: prefix, got %q", parsed)
	}
}

func TestParseSqliteConnectionString_ShouldReturnAbsoluteDBPath(t *testing.T) {
	t.Parallel()

	_, dbPath, _, err := parseSqliteConnectionString("test.db")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !filepath.IsAbs(dbPath) {
		t.Errorf("expected dbPath to be absolute, got %q", dbPath)
	}
}

func TestParseSqliteConnectionString_ShouldReportIsMemoryDB(t *testing.T) {
	t.Parallel()

	_, _, isMemory, err := parseSqliteConnectionString(":memory:")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !isMemory {
		t.Error("expected isMemoryDB to be true for :memory:")
	}
}

func TestParseSqliteConnectionString_ShouldSetForeignKeysPragma(t *testing.T) {
	t.Parallel()

	parsed, _, _, err := parseSqliteConnectionString("test.db")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	u, err := url.Parse(parsed)
	if err != nil {
		t.Fatalf("failed to parse resulting connection string: %v", err)
	}

	found := false
	for _, p := range u.Query()["_pragma"] {
		if strings.HasPrefix(strings.ToLower(p), "foreign_keys") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected foreign_keys pragma to be set, got query: %v", u.Query())
	}
}

func TestParseSqliteConnectionString_ShouldErrorOnForbiddenForeignKeysPragmaOverride(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseSqliteConnectionString("test.db?_pragma=foreign_keys(0)")
	if err == nil {
		t.Fatal("expected an error when _pragma=foreign_keys is explicitly set, got nil")
	}
}

func TestConvertSqlitePragmaArgs_ShouldTranslateLegacyBusyTimeoutOption(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	convertSqlitePragmaArgs(u)

	found := false
	for _, p := range u.Query()["_pragma"] {
		if p == "busy_timeout(5000)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected _busy_timeout to be translated to a busy_timeout pragma, got query: %v", u.Query())
	}
}

func TestConvertSqlitePragmaArgs_ShouldPassThroughUnrecognizedParams(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?cache=shared")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	convertSqlitePragmaArgs(u)

	if got := u.Query().Get("cache"); got != "shared" {
		t.Errorf("expected unrecognized param 'cache' to pass through unchanged, got %q", got)
	}
}

func TestAddSqliteDefaultParameters_ShouldSetWALJournalModeByDefault(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found := false
	for _, p := range u.Query()["_pragma"] {
		if p == "journal_mode(WAL)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected journal_mode(WAL) pragma for a non-memory, non-readonly DB, got: %v", u.Query()["_pragma"])
	}
}

func TestAddSqliteDefaultParameters_ShouldSetMemoryJournalModeForInMemoryDB(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file::memory:")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, true); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found := false
	for _, p := range u.Query()["_pragma"] {
		if p == "journal_mode(MEMORY)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected journal_mode(MEMORY) pragma for an in-memory DB, got: %v", u.Query()["_pragma"])
	}
}

func TestAddSqliteDefaultParameters_ShouldSetDeleteJournalModeForReadOnlyDB(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?mode=ro")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found := false
	for _, p := range u.Query()["_pragma"] {
		if p == "journal_mode(DELETE)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected journal_mode(DELETE) pragma for a read-only DB, got: %v", u.Query()["_pragma"])
	}
}

func TestAddSqliteDefaultParameters_ShouldNotOverrideExplicitJournalMode(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_pragma=journal_mode(TRUNCATE)")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	pragmas := u.Query()["_pragma"]
	count := 0
	for _, p := range pragmas {
		if strings.HasPrefix(strings.ToLower(p), "journal_mode") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one journal_mode pragma to remain, got %d: %v", count, pragmas)
	}
}

func TestAddSqliteDefaultParameters_ShouldNotOverrideExplicitBusyTimeout(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_pragma=busy_timeout(1000)")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	pragmas := u.Query()["_pragma"]
	count := 0
	for _, p := range pragmas {
		if strings.HasPrefix(strings.ToLower(p), "busy_timeout") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one busy_timeout pragma to remain, got %d: %v", count, pragmas)
	}
}

func TestAddSqliteDefaultParameters_ShouldErrorWhenForeignKeysPragmaExplicitlySet(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err == nil {
		t.Fatal("expected an error when _pragma=foreign_keys is explicitly set, got nil")
	}
}

func TestAddSqliteDefaultParameters_ShouldDefaultTxlockToImmediate(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := u.Query().Get("_txlock"); got != "immediate" {
		t.Errorf("got _txlock %q, want %q", got, "immediate")
	}
}

func TestIsWritableDir_ShouldReturnTrueForWritableTempDir(t *testing.T) {
	t.Parallel()

	ok, err := IsWritableDir(t.TempDir())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ok {
		t.Error("expected a fresh temp dir to be writable")
	}
}

func TestIsWritableDir_ShouldReturnFalseWhenDirDoesNotExist(t *testing.T) {
	t.Parallel()

	ok, err := IsWritableDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ok {
		t.Error("expected a nonexistent directory to be reported as not writable")
	}
}

func TestIsWritableDir_ShouldReturnFalseWhenPathIsAFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	ok, err := IsWritableDir(filePath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ok {
		t.Error("expected a file path (not a directory) to be reported as not writable")
	}
}

func TestIsWritableDir_ShouldReturnFalseForReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("failed to chmod test dir: %v", err)
	}
	// Restore permissions so t.TempDir()'s own cleanup can remove it.
	// Not parallel: mutates filesystem permissions that a concurrent test
	// touching the same OS user's umask expectations could be surprised by,
	// and root-run test suites make this assertion unreliable.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks don't apply")
	}

	ok, err := IsWritableDir(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ok {
		t.Error("expected a read-only directory to be reported as not writable")
	}
}

func TestEnsureSqliteDatabaseDir_ShouldCreateMissingDir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dbPath := filepath.Join(base, "nested", "sub", "test.db")

	if err := ensureSqliteDatabaseDir(dbPath); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	info, err := os.Stat(filepath.Dir(dbPath))
	if err != nil {
		t.Fatalf("expected parent directory to exist, got error: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected parent path to be a directory")
	}
}

func TestEnsureSqliteDatabaseDir_ShouldSucceedWhenDirAlreadyExists(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dbPath := filepath.Join(base, "test.db")

	if err := ensureSqliteDatabaseDir(dbPath); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestEnsureSqliteDatabaseDir_ShouldErrorWhenParentPathIsAFile(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	filePath := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	dbPath := filepath.Join(filePath, "test.db")

	if err := ensureSqliteDatabaseDir(dbPath); err == nil {
		t.Fatal("expected an error when the parent path is a file, got nil")
	}
}

func TestEnsureSqliteTempDir_ShouldSkipWhenSqliteTmpdirEnvSet(t *testing.T) {
	// Reads/writes process environment variables, so it must not run in
	// parallel with other tests touching SQLITE_TMPDIR or TMPDIR.
	t.Setenv("SQLITE_TMPDIR", "/some/preexisting/value")

	if err := ensureSqliteTempDir(t.TempDir()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := os.Getenv("SQLITE_TMPDIR"); got != "/some/preexisting/value" {
		t.Errorf("expected SQLITE_TMPDIR to be left untouched, got %q", got)
	}
}

func TestEnsureSqliteTempDir_ShouldSkipWhenTmpdirEnvSet(t *testing.T) {
	// Reads/writes process environment variables, so it must not run in
	// parallel with other tests touching SQLITE_TMPDIR or TMPDIR.
	//
	// t.TempDir() must be created before TMPDIR is overridden, since
	// t.TempDir() itself consults TMPDIR to pick a base directory.
	dir := t.TempDir()

	t.Setenv("SQLITE_TMPDIR", "")
	t.Setenv("TMPDIR", "/some/preexisting/value")

	if err := ensureSqliteTempDir(dir); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := os.Getenv("SQLITE_TMPDIR"); got != "" {
		t.Errorf("expected SQLITE_TMPDIR to be left unset when TMPDIR already covers it, got %q", got)
	}
}

func TestEnsureSqliteTempDir_ShouldFindWritableSystemTempDir(t *testing.T) {
	// Reads/writes process environment variables, so it must not run in
	// parallel with other tests touching SQLITE_TMPDIR or TMPDIR.
	//
	// t.TempDir() must be created before TMPDIR is overridden, since
	// t.TempDir() itself consults TMPDIR to pick a base directory.
	dir := t.TempDir()

	t.Setenv("SQLITE_TMPDIR", "")
	t.Setenv("TMPDIR", "")

	// With neither env var set, the fallback loop probes /var/tmp, /usr/tmp
	// and /tmp; at least one of those is writable on any machine this test
	// suite runs on, so SQLITE_TMPDIR must stay unset.
	if err := ensureSqliteTempDir(dir); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := os.Getenv("SQLITE_TMPDIR"); got != "" {
		t.Errorf("expected SQLITE_TMPDIR to stay unset when a system temp dir is writable, got %q", got)
	}
}

func TestConvertSqlitePragmaArgs_ShouldTranslateAllLegacyOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		wantPragma string
	}{
		{"should translate _auto_vacuum", "_auto_vacuum=full", "auto_vacuum(full)"},
		{"should translate _vacuum alias", "_vacuum=incremental", "auto_vacuum(incremental)"},
		{"should translate _timeout alias", "_timeout=750", "busy_timeout(750)"},
		{"should translate _case_sensitive_like", "_case_sensitive_like=true", "case_sensitive_like(true)"},
		{"should translate _cslike alias", "_cslike=false", "case_sensitive_like(false)"},
		{"should translate _foreign_keys", "_foreign_keys=1", "foreign_keys(1)"},
		{"should translate _fk alias", "_fk=0", "foreign_keys(0)"},
		{"should translate _locking_mode", "_locking_mode=exclusive", "locking_mode(exclusive)"},
		{"should translate _locking alias", "_locking=normal", "locking_mode(normal)"},
		{"should translate _secure_delete", "_secure_delete=fast", "secure_delete(fast)"},
		{"should translate _synchronous", "_synchronous=normal", "synchronous(normal)"},
		{"should translate _sync alias", "_sync=off", "synchronous(off)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse("file:test.db?" + tt.query)
			if err != nil {
				t.Fatalf("failed to parse test URL: %v", err)
			}

			convertSqlitePragmaArgs(u)

			found := false
			for _, p := range u.Query()["_pragma"] {
				if p == tt.wantPragma {
					found = true
				}
			}
			if !found {
				t.Errorf("expected pragma %q, got query: %v", tt.wantPragma, u.Query())
			}
		})
	}
}

func TestAddSqliteDefaultParameters_ShouldSetDeleteJournalModeForImmutableDB(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?immutable=1")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	found := false
	for _, p := range u.Query()["_pragma"] {
		if p == "journal_mode(DELETE)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected journal_mode(DELETE) pragma for an immutable DB, got: %v", u.Query()["_pragma"])
	}
}

func TestAddSqliteDefaultParameters_ShouldKeepExplicitImmediateTxlock(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_txlock=IMMEDIATE")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := u.Query()["_txlock"]; len(got) != 1 || got[0] != "immediate" {
		t.Errorf("expected a single lowercased 'immediate' _txlock value, got %v", got)
	}
}

func TestAddSqliteDefaultParameters_ShouldKeepNonImmediateTxlockWithWarning(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("file:test.db?_txlock=deferred")
	if err != nil {
		t.Fatalf("failed to parse test URL: %v", err)
	}

	if err := addSqliteDefaultParameters(u, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// The non-recommended value is warned about but must not be overridden.
	if got := u.Query()["_txlock"]; len(got) != 1 || got[0] != "deferred" {
		t.Errorf("expected the explicit 'deferred' _txlock value to be kept, got %v", got)
	}
}

func TestEnsureSqliteDatabaseDir_ShouldErrorWhenDirCreationFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks don't apply")
	}

	base := t.TempDir()
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatalf("failed to chmod test dir: %v", err)
	}
	// Restore permissions so t.TempDir()'s own cleanup can remove it.
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })

	// The parent is traversable but not writable: stat reports the target
	// directory as missing, and MkdirAll then fails with a permission error.
	dbPath := filepath.Join(base, "sub", "test.db")

	if err := ensureSqliteDatabaseDir(dbPath); err == nil {
		t.Fatal("expected an error when the directory cannot be created, got nil")
	}
}

func TestEnsureSqliteDatabaseDir_ShouldErrorWhenStatFails(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	filePath := filepath.Join(base, "blocker")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// A path routed *through* a regular file makes stat fail with ENOTDIR,
	// which is neither success nor not-exist.
	dbPath := filepath.Join(filePath, "sub", "test.db")

	if err := ensureSqliteDatabaseDir(dbPath); err == nil {
		t.Fatal("expected an error when the directory cannot be checked, got nil")
	}
}

func TestIsWritableDir_ShouldErrorWhenStatFails(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	filePath := filepath.Join(base, "blocker")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// A path routed through a regular file fails stat with ENOTDIR rather
	// than not-exist.
	if _, err := IsWritableDir(filepath.Join(filePath, "sub")); err == nil {
		t.Fatal("expected an error when the directory cannot be checked, got nil")
	}
}

// normalizeQuery runs the custom SQLite normalize() function registered by
// registerSqliteFunctions against a fresh in-memory database, passing args
// through as SQL parameters.
func normalizeQuery(t *testing.T, arg0 any, arg1 any) (string, error) {
	t.Helper()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}

	var got string
	err = sqlDB.QueryRow("SELECT normalize(?, ?)", arg0, arg1).Scan(&got)
	return got, err
}

func TestSqliteNormalizeFunction_ShouldNormalizeUnicodeForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		form  string
		want  string
	}{
		{"should compose combining accent when form nfc", "é", "nfc", "é"},
		{"should decompose precomposed accent when form nfd", "é", "nfd", "é"},
		{"should accept uppercase form names", "é", "NFC", "é"},
		{"should replace ligature when form nfkc", "ﬁ", "nfkc", "fi"},
		{"should replace ligature when form nfkd", "ﬁ", "nfkd", "fi"},
		{"should return empty string unchanged", "", "nfc", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeQuery(t, tt.input, tt.form)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Errorf("normalize(%q, %q) = %q, want %q", tt.input, tt.form, got, tt.want)
			}
		})
	}
}

func TestSqliteNormalizeFunction_ShouldErrorOnInvalidArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		arg0 any
		arg1 any
	}{
		{"should error when form unsupported", "text", "nfx"},
		{"should error when first argument not a string", int64(42), "nfc"},
		{"should error when second argument not a string", "text", int64(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := normalizeQuery(t, tt.arg0, tt.arg1); err == nil {
				t.Fatal("expected a query error, got nil")
			}
		})
	}
}
