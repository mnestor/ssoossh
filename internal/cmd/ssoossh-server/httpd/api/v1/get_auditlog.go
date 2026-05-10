// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"net/http"

	"github.com/go-chi/render"
	types "github.com/mnestor/ssoossh/internal/api/response_types"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/store"
)

func apiGetAuditLog(w http.ResponseWriter, r *http.Request) {
	username := mware.GetUserName(r)
	auditStore := r.Context().Value(store.AuditLogContext).(store.AuditLogInterface)

	entries, err := auditStore.ListByUser(username)
	if err != nil {
		render.Status(r, http.StatusInternalServerError)
		_ = render.Render(w, r, types.NewResponseError("error", "failed to retrieve audit log"))
		return
	}

	resp := make([]types.AuditLogEntryResponse, 0, len(entries))
	for _, e := range entries {
		resp = append(resp, types.AuditLogEntryResponse{
			ID:        e.ID,
			RequestID: e.RequestID,
			Decision:  e.Decision,
			Account:   e.Account,
			CertType:  e.CertType,
			CreatedAt: e.CreatedAt,
		})
	}

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, types.NewAuditLogResponse(resp))
}
