// Created by Mike Nestor <me@mikenestor.org>
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

var (
	router *chi.Mux = chi.NewRouter()
)

func init() {
	router.Use(render.SetContentType(render.ContentTypeJSON))
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1")
			next.ServeHTTP(w, r)
		})
	})
}

func GetRouter() *chi.Mux {
	return router
}
