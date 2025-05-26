package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/config"
	"golang.org/x/oauth2"
)

var (
	authConfig *oauth2.Config
	verifier   *oidc.IDTokenVerifier
	// provider        *oidc.Provider
)

func SetupRoutes(r *chi.Mux) {
	router := chi.NewRouter()
	router.Get("/oauth", loginCallback)
	router.Get("/", loginOauth)
	r.Mount("/login", router)

	c := config.GetConfig().Server

	// log.Info("%+v", c)
	provider, err := oidc.NewProvider(context.Background(), c.AuthConfig.ProviderUrl)
	if err != nil {
		log.Printf("could not create provider: %s", err.Error())
	}

	oidcConfig := &oidc.Config{
		ClientID: c.AuthConfig.ClientID,
	}
	verifier = provider.Verifier(oidcConfig)

	redirectUrl, _ := url.Parse(fmt.Sprintf("https://%s", c.Domain))
	redirectUrl.Path = path.Join(redirectUrl.Path, "/login/oauth")

	authConfig = &oauth2.Config{
		RedirectURL:  redirectUrl.String(),
		ClientID:     c.AuthConfig.ClientID,
		ClientSecret: c.AuthConfig.ClientSecret,
		Scopes:       []string{oidc.ScopeOpenID, c.AuthConfig.Scopes},
		Endpoint:     provider.Endpoint(),
	}
}

func randStringBytes(nByte int) (string, error) {
	b := make([]byte, nByte)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func loginOauth(w http.ResponseWriter, r *http.Request) {
	state, err := randStringBytes(16)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randStringBytes(16)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "state",
		Value:    state,
		MaxAge:   int(time.Hour.Seconds() * 10),
		Secure:   r.TLS != nil,
		HttpOnly: true,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "nonce",
		Value:    nonce,
		MaxAge:   int(time.Hour.Seconds() * 10),
		Secure:   r.TLS != nil,
		HttpOnly: true,
	})

	url := authConfig.AuthCodeURL(state, oidc.Nonce(nonce))
	// slog.Debug("Redirecting", slog.String("url", url))
	http.Redirect(w, r, url, http.StatusFound)
}

func loginCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	oauth2Token, err := authConfig.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		slog.Error("Failed to exchange token", slog.Any("error", err))
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		http.Error(w, "No id_token field in oauth2 token.", http.StatusInternalServerError)
		return
	}

	slog.Debug("Unsafe", slog.String("token", rawIDToken))

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "Failed to verify ID Token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	nonce, err := r.Cookie("nonce")
	if err != nil {
		http.Error(w, "nonce not found", http.StatusBadRequest)
		return
	}
	if idToken.Nonce != nonce.Value {
		http.Error(w, "nonce did not match", http.StatusBadRequest)
		return
	}

	var claims map[string]interface{}

	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	c := config.GetConfig().Server.AuthConfig.Fields
	usernameR, ok := claims[c.Username]
	if !ok {
		slog.Error("Oauth response did not have a username", slog.Any("id_token", claims))
		return
	}
	username := usernameR.(string)

	// _, _ = w.Write([]byte(fmt.Sprintf("%+v", claims)))

	var groups []string
	if c.Groups != "" {
		groupsR, ok := claims[c.Groups]
		if !ok {
			slog.Error("Oauth response did not have a group we expected", slog.Any("id_token", claims))
			return
		}
		groupsI := groupsR.([]interface{})
		for _, v := range groupsI {
			groups = append(groups, v.(string))
		}
	}

	middleware.GetSession(r).Put(r.Context(), "username", username)
	middleware.GetSession(r).Put(r.Context(), "groups", groups)

	// log.Println(claims.Roles)
	enforcer := GetEnforcerFromContext(r)
	enforcer.AddRoles(r, username, []string{"user"})

	preLoginPath := middleware.GetSession(r).GetString(r.Context(), "prelogin")
	if preLoginPath != "" {
		http.Redirect(w, r, preLoginPath, http.StatusFound)
		return
	}

	// w.WriteHeader(http.StatusFound)
	http.Redirect(w, r, "/", http.StatusFound)
}
