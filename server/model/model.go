// Package model defines the database structs that back ssoosshd's
// persisted state, one struct per table. Struct tags target GORM (used as
// the query layer only — GORM's AutoMigrate is not used, see server/CLAUDE.md
// and .claude/rules/go.md). A field added here must also be added to both
// migration files under server/resources/migrations.
package model
