//go:build dbparity

package service

import (
	"testing"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/postgres"
)

// should delete oldest first whatever the action on Postgres too, where
// created_at is a TIMESTAMPTZ. The cases share one database, so the table is
// emptied before each. Not parallel: the Postgres harness binds one fixed
// port.
func TestSweepAuditEvents_shouldDeleteOldestFirstWhateverTheActionOnPostgres(t *testing.T) {
	db, _ := postgres.ConnectAndMigrate(t, t.Context())
	for _, tt := range refusalSweepCases() {
		t.Run(tt.name, func(t *testing.T) {
			if err := db.Where("1 = 1").Delete(&model.AuditEvent{}).Error; err != nil {
				t.Fatalf("empty the audit table: %v", err)
			}
			checkRefusalSweep(t, db, tt)
		})
	}
}
