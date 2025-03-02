// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/render"
	"github.com/mnestor/ssoossh/internal/api/types"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/store"
)

func init() {
	router.Get("/certificate", apiGetCertificate)
}

func apiGetCertificate(w http.ResponseWriter, r *http.Request) {
	timeout := 120 * time.Second
	ctxEnd, ctxCancel := context.WithTimeout(r.Context(), timeout)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	id := mware.GetSession(r).GetString(r.Context(), "signreq")

	// clear it
	mware.GetSession(r).Put(r.Context(), "signreq", "")

	s := r.Context().Value(
		store.CertificateContext).(*store.MemoryCertificatesStore)

	// TODO: just get redis or postgres for pubsub idiot
	ticker := time.NewTicker(1 * time.Second)

	cleanup := func() {
		_ = s.Delete(id)
		ticker.Stop()
		ctxCancel()
	}

	for {
		select {
		case <-ticker.C:
			slog.Info("tick")
			cert, err := s.Get(id)
			if err != nil {
				continue
			}

			// we have the cert now
			render.Status(r, http.StatusOK)
			_ = render.Render(w, r, types.NewCertificateRequestResponse("success", cert.Certificate))
			cleanup()
			return

		// parent timeout is slightly longer so we can get the fail response out first
		case <-ctxEnd.Done():
			slog.Info("ctxEnd")
			http.Error(w, "Request timed out", http.StatusRequestTimeout)
			cleanup()
			return
		}
	}
}
