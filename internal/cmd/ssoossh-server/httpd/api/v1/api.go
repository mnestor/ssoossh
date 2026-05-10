// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func NewRouter() *chi.Mux {
	router := chi.NewRouter()
	router.Use(render.SetContentType(render.ContentTypeJSON))
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1")
			next.ServeHTTP(w, r)
		})
	})

	router.Get("/ca", apiGetCAs)
	router.Get("/certificate", apiGetCertificate)
	router.Post("/signreq", apiSignRequestPost)
	router.Get("/audit", apiGetAuditLog)

	return router
}
