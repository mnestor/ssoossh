// Created by Mike Nestor <me@mikenestor.org>
package middleware

import (
	"net/http"

	"github.com/mnestor/ssoossh/internal/config"
)

func Hsts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := config.GetConfig().Server
		if c.Hsts != "" {
			w.Header().Set("Strict-Transport-Security", c.Hsts)
		}
		next.ServeHTTP(w, r)
	})
}
