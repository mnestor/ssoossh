// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
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
	ctxWait, ctxCancel := context.WithCancel(r.Context())
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

	done := false
	go func() {
		for _ = range time.Tick(1 * time.Second) {
			if done {
				ctxCancel()
				return
			}
			cert, err := s.Get(id)
			if err != nil {
				time.Sleep(1 * time.Second)

				continue
			}
			_ = s.Delete(id)
			// we have the cert now
			render.Status(r, http.StatusOK)
			_ = render.Render(w, r, types.NewCertificateRequestResponse("success", cert.Certificate))
			ctxCancel()
			return
		}
	}()

	// hold open until client disconnects
	<-ctxWait.Done()
	done = true // make sure the loop ends
}
