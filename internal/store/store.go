// Created by Mike Nestor <me@mikenestor.org>
package store

import (
	"time"
)

type ContextTypes int

const (
	CertRequestContext ContextTypes = iota
	CertificateContext
)

type CertRequest struct {
	ID        string
	Pubkey    string
	Type      string
	CreatedAt time.Time
}

type Certificate struct {
	ID          string
	Certificate string
	CreatedAt   time.Time
}

type CertRequestInterface interface {
	Get(id string) (CertRequest, error)
	Create(pubKey CertRequest) error
	Delete(id string) error
}

type CertificateInterface interface {
	Get(id string) (Certificate, error)
	Create(cert Certificate) error
	Delete(id string) error
}
