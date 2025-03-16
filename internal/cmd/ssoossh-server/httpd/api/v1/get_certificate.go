// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/render"
	types "github.com/mnestor/ssoossh/internal/api/response_types"
	mware "github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/store"
)

func apiGetCertificate(w http.ResponseWriter, r *http.Request) {
	timeout := 90 * time.Second
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

	cleanup := func() {
		_ = s.Delete(id)
		ctxCancel()
	}
	blockedCertChan := make(chan bool)

	go func() {
		_ = *s.GetWait(id)
		blockedCertChan <- true
	}()

	for {
		select {
		case <-blockedCertChan:
			cert, _ := s.Get(id)
			// we have the cert now
			render.Status(r, http.StatusOK)
			_ = render.Render(w, r, types.NewCertificateRequestResponse("success", cert.Certificate))
			cleanup()
			// case <-ticker.C:
			// 	slog.Info("tick")
			// 	cert, err := s.Get(id)
			// 	if err != nil {
			// 		continue
			// 	}

			return

		// parent timeout is slightly longer so we can get the fail response out first
		case <-ctxEnd.Done():
			// http.Error(w, "Request timed out", http.StatusRequestTimeout)
			render.Status(r, http.StatusRequestTimeout)
			_ = render.Render(w, r, types.NewResponseError("timeout", "Request timed out waiting for approval"))
			cleanup()
			return
		}
	}
}
