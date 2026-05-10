// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type GormAuditLogStore struct {
	db *gorm.DB
}

func NewGormAuditLogStore(db *gorm.DB) (*GormAuditLogStore, error) {
	if err := db.AutoMigrate(&AuditLogEntry{}); err != nil {
		return nil, err
	}
	return &GormAuditLogStore{db: db}, nil
}

func (s *GormAuditLogStore) Create(entry *AuditLogEntry) error {
	entry.ID = uuid.New().String()
	entry.CreatedAt = time.Now().UTC()
	return s.db.Create(entry).Error
}

func (s *GormAuditLogStore) ListByUser(username string) ([]AuditLogEntry, error) {
	var entries []AuditLogEntry
	err := s.db.Where("user_name = ?", username).
		Order("created_at desc").
		Find(&entries).Error
	return entries, err
}
