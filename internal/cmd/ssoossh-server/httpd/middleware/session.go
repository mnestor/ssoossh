// Created by Mike Nestor <me@mikenestor.org>
package middleware

import (
	"context"
	"encoding/gob"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
)

type SessionManager struct {
	*scs.SessionManager
}

type ContextTypes int

const (
	SessionContext ContextTypes = iota
)

type UserInfo struct {
	FullName string `json:"nickname"`
	Username string `json:"preferred_username"`
	Email    string `json:"email"`
}

func init() {
	gob.Register(UserInfo{})
}

func NewSessionManager() *SessionManager {
	sessionManager := scs.New()
	sessionManager.Lifetime = 24 * time.Hour
	return &SessionManager{sessionManager}
}

func (m *SessionManager) RegisterHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, SessionContext, m)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func GetSession(r *http.Request) *SessionManager {
	s := r.Context().Value(SessionContext)
	if s != nil {
		return s.(*SessionManager)
	}

	slog.Error("we are missing a session context")

	return nil
}

func GetUserName(r *http.Request) string {
	username := GetSession(r).GetString(r.Context(), "username")
	if username == "" {
		return "anonymous"
	}

	return username
}

func GetGroups(r *http.Request) []string {
	groupsR := GetSession(r).Get(r.Context(), "groups")
	if groupsR == nil {
		return []string{}
	}
	groups := groupsR.([]string)

	return groups
}
