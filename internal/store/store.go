// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"time"
)

type ContextTypes int

const (
	CertRequestContext ContextTypes = iota
	CertificateContext
	AuditLogContext
)

type CertRequest struct {
	ID        string
	Pubkey    string
	Type      string
	CreatedAt time.Time
	Account   string
}

type Certificate struct {
	ID          string
	Certificate string
	CreatedAt   time.Time
}

type Subscriber struct {
	ID    string
	Phone chan error
}

type AuditLogEntry struct {
	ID        string `gorm:"primaryKey"`
	RequestID string
	UserName  string
	Decision  string // "approved" | "rejected"
	PublicKey string
	Account   string
	CertType  string
	CreatedAt time.Time
}

type CertRequestInterface interface {
	Get(id string) (*CertRequest, error)
	Create(c *CertRequest) error
	Delete(id string) error
}

type CertificateInterface interface {
	Get(id string) (*Certificate, error)
	Create(c *Certificate) error
	Delete(id string) error
	GetWait(id string) *Subscriber
	Reject(id string) error
}

type AuditLogInterface interface {
	Create(entry *AuditLogEntry) error
	ListByUser(username string) ([]AuditLogEntry, error)
}
