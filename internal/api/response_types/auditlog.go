// Created by Mike Nestor <me@mikenestor.org>
package types

import "time"

type AuditLogEntryResponse struct {
	*ResponseRender
	ID        string    `json:"id"`
	RequestID string    `json:"request_id"`
	Decision  string    `json:"decision"`
	Account   string    `json:"account"`
	CertType  string    `json:"cert_type"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditLogResponse struct {
	*ResponseRender
	StatusText string                  `json:"status"`
	Entries    []AuditLogEntryResponse `json:"entries"`
}

func NewAuditLogResponse(entries []AuditLogEntryResponse) *AuditLogResponse {
	return &AuditLogResponse{
		StatusText: "success",
		Entries:    entries,
	}
}
