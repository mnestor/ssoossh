// Created by Mike Nestor <me@mikenestor.org>
package middleware

import (
	"net/http"

	"github.com/mnestor/ssoossh/internal/config"
)

func Sni(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := config.GetConfig().Server
		ctx := r.Context()
		if c.Domain == r.Host {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		http.Error(w, "Bad Host", http.StatusInternalServerError)
	})
}
