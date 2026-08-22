// Package dbtime keeps every timestamp that reaches the database in UTC.
//
// This is not cosmetic. The SQLite driver this project uses
// (github.com/glebarez/go-sqlite) binds a time.Time by formatting it to
// text, offset and all, and the DATETIME column declaration carries NUMERIC
// affinity, which leaves a non-numeric string as TEXT. Every `<`, `>`, and
// `ORDER BY` against such a column is therefore a BINARY-collation string
// comparison, not a chronological one. Two instants written from different
// UTC offsets compare by their literal digits:
//
//	"2026-08-22T06:00:00-04:00"  <  "2026-08-22T10:30:00Z"   // string: true
//	                                                         // instant: false
//
// So a cutoff expressed in one zone silently matches rows it should not,
// and ORDER BY returns rows out of chronological order. Postgres is
// unaffected — TIMESTAMPTZ stores a true instant — which is exactly what
// makes the bug easy to miss: it only shows up on one of the two supported
// backends, and only once the offsets actually differ (a DST transition, a
// TZ change, or a caller that normalizes one side but not the other).
//
// Normalizing to UTC fixes it outright: every stored value then ends in
// "Z", so lexicographic order and chronological order coincide.
//
// The invariant has two halves, and both are needed — normalizing only what
// is written still breaks if a query compares it against a local-offset
// bound parameter:
//
//   - Values written through GORM. Handled here, by Plugin, so a new write
//     path cannot reintroduce the bug by forgetting a .UTC() call.
//   - Values used as query parameters. GORM builds its bound parameters
//     inside the query callback itself, with no hook in between, so these
//     cannot be intercepted generically. Callers that compare against a
//     time column must pass a UTC value; see CertRequestService.ttlCutoff
//     and strandedCutoff, and the regression tests in dbtime_test.go and
//     server/service/sweep_test.go.
package dbtime

import (
	"reflect"
	"time"

	"gorm.io/gorm"
)

// timeType is compared against field types to spot time.Time values without
// paying for an interface assertion per field.
var timeType = reflect.TypeOf(time.Time{})

// Plugin normalizes every time.Time GORM writes to UTC, on both create and
// update, whether the statement's destination is a struct, a slice of
// structs, or the map[string]any form of Updates.
//
// Register it with db.Use(dbtime.Plugin{}) — and pair it with
// gorm.Config.NowFunc (see NowFunc), which covers the timestamps GORM
// generates for itself rather than the ones callers supply.
type Plugin struct{}

// Name identifies the plugin to GORM, which rejects duplicate registration
// under the same name.
func (Plugin) Name() string { return "ssoossh:dbtime" }

// Initialize registers the normalizing callback ahead of GORM's own create
// and update callbacks, which is the last point at which the statement's
// values can still be rewritten before the SQL is built.
func (p Plugin) Initialize(db *gorm.DB) error {
	if err := db.Callback().Create().Before("gorm:create").
		Register("ssoossh:dbtime:create", normalizeStatement); err != nil {
		return err
	}
	return db.Callback().Update().Before("gorm:update").
		Register("ssoossh:dbtime:update", normalizeStatement)
}

// NowFunc is the gorm.Config.NowFunc this project uses. GORM stamps its own
// autoCreateTime/autoUpdateTime fields from inside the create callback,
// after Plugin's callback has already run, so those values would otherwise
// escape normalization.
func NowFunc() time.Time { return time.Now().UTC() }

// normalizeStatement rewrites the time values on the statement about to be
// executed. Both destination shapes GORM uses are covered: the map form
// produced by Updates(map[string]any{...}), and the struct (or slice of
// structs) form reflected into ReflectValue by Create/Save/Updates.
func normalizeStatement(db *gorm.DB) {
	if db.Statement == nil {
		return
	}

	// Updates(map[string]any{"resolved_at": t, ...}) never reaches
	// ReflectValue as a struct, so it has to be handled on its own.
	if m, ok := db.Statement.Dest.(map[string]any); ok {
		for k, v := range m {
			if t, ok := v.(time.Time); ok {
				m[k] = t.UTC()
			} else if tp, ok := v.(*time.Time); ok && tp != nil {
				utc := tp.UTC()
				m[k] = &utc
			}
		}
		return
	}

	normalizeValue(db.Statement.ReflectValue)
}

// normalizeValue walks rv, converting every time.Time it can address to
// UTC. Slices and arrays are walked element-wise so batch creates are
// covered too.
func normalizeValue(rv reflect.Value) {
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := range rv.Len() {
			normalizeValue(rv.Index(i))
		}
	case reflect.Struct:
		// A time.Time is itself a struct; convert it rather than
		// descending into its unexported fields.
		if rv.Type() == timeType {
			setUTC(rv)
			return
		}
		for i := range rv.NumField() {
			normalizeValue(rv.Field(i))
		}
	}
}

// setUTC rewrites an addressable time.Time in place. A value that GORM
// handed us unaddressable (a map element, say) is left alone rather than
// panicking: the map form is already handled by normalizeStatement.
func setUTC(rv reflect.Value) {
	if !rv.CanSet() {
		return
	}
	t, ok := rv.Interface().(time.Time)
	if !ok || t.IsZero() {
		return
	}
	rv.Set(reflect.ValueOf(t.UTC()))
}
