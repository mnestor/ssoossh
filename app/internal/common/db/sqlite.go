package db

// functions here are adapted from
// - https://github.com/pocket-id/pocket-id/blob/main/backend/internal/bootstrap/db_bootstrap.go
//    Copyright (c) 2024, Elias Schneider
//    BSD 2-Clause License
// - https://github.com/dapr/components-contrib/blob/main/common/authentication/sqlite/metadata.go
//    Copyright (C) 2023 The Dapr Authors
//    License: Apache2

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	sqlitelib "github.com/glebarez/go-sqlite"
	"golang.org/x/text/unicode/norm"
)

func parseSqliteConnectionString(connString string) (parsedConnString string, isMemoryDB bool, err error) {
	if !strings.HasPrefix(connString, "file:") {
		connString = "file:" + connString
	}

	// Check if we're using an in-memory database
	isMemoryDB = isSqliteInMemory(connString)

	// Parse the connection string
	connStringUrl, err := url.Parse(connString)
	if err != nil {
		return "", false, fmt.Errorf("failed to parse SQLite connection string: %w", err)
	}

	// Add the default and required params
	err = addSqliteDefaultParameters(connStringUrl, isMemoryDB)
	if err != nil {
		return "", false, fmt.Errorf("invalid SQLite connection string: %w", err)
	}

	// Get the absolute path to the database
	// Here, we know for a fact that the ? is present
	parsedConnString = connStringUrl.String()

	return parsedConnString, isMemoryDB, nil
}

// Adds the default (and some required) parameters to the SQLite connection string.
// Note this function updates connStringUrl.
func addSqliteDefaultParameters(connStringUrl *url.URL, isMemoryDB bool) error {
	// This function include code adapted from https://github.com/dapr/components-contrib/blob/v1.14.6/
	// Copyright (C) 2023 The Dapr Authors
	// License: Apache2
	const defaultBusyTimeout = 2500 * time.Millisecond

	// Get the "query string" from the connection string if present
	qs := connStringUrl.Query()
	if len(qs) == 0 {
		qs = make(url.Values, 2)
	}

	// Check if the database is read-only or immutable
	isReadOnly := false
	if len(qs["mode"]) > 0 {
		// Keep the first value only
		qs["mode"] = []string{
			strings.ToLower(qs["mode"][0]),
		}
		if qs["mode"][0] == "ro" {
			isReadOnly = true
		}
	}
	if len(qs["immutable"]) > 0 {
		// Keep the first value only
		qs["immutable"] = []string{
			strings.ToLower(qs["immutable"][0]),
		}
		if qs["immutable"][0] == "1" {
			isReadOnly = true
		}
	}

	// We do not want to override a _txlock if set, but we'll show a warning if it's not "immediate"
	if len(qs["_txlock"]) > 0 {
		// Keep the first value only
		qs["_txlock"] = []string{
			strings.ToLower(qs["_txlock"][0]),
		}
		if qs["_txlock"][0] != "immediate" {
			slog.Warn("SQLite connection is being created with a _txlock different from the recommended value 'immediate'")
		}
	} else {
		qs["_txlock"] = []string{"immediate"}
	}

	// Add pragma values
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
		switch {
		case isMemoryDB:
			// For in-memory databases, set the journal to MEMORY, the only allowed option besides OFF (which would make transactions ineffective)
			qs["_pragma"] = append(qs["_pragma"], "journal_mode(MEMORY)")
		case isReadOnly:
			// Set the journaling mode to "DELETE" (the default) if the database is read-only
			qs["_pragma"] = append(qs["_pragma"], "journal_mode(DELETE)")
		default:
			// Enable WAL
			qs["_pragma"] = append(qs["_pragma"], "journal_mode(WAL)")
		}
	}

	// Forcefully enable foreign keys
	qs["_pragma"] = append(qs["_pragma"], "foreign_keys(1)")

	// Update the connStringUrl object
	connStringUrl.RawQuery = qs.Encode()

	return nil
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
	qs, _ := url.ParseQuery(lc[(idx + 1):])

	return len(qs["mode"]) > 0 && qs["mode"][0] == "memory"
}

func registerSqliteFunctions() {
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

		var form norm.Form
		switch strings.ToLower(arg1) {
		case "nfc":
			form = norm.NFC
		case "nfd":
			form = norm.NFD
		case "nfkc":
			form = norm.NFKC
		case "nfkd":
			form = norm.NFKD
		default:
			return nil, fmt.Errorf("unsupported form: %s", arg1)
		}

		if len(arg0) == 0 {
			return arg0, nil
		}

		return form.String(arg0), nil
	})
}
