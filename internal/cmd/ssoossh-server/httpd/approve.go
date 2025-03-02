// Created by Mike Nestor <me@mikenestor.org>
package httpd

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/mnestor/ssoossh/internal/api/types"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/api/v1"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/config"
	"github.com/mnestor/ssoossh/internal/nonce"
	"github.com/mnestor/ssoossh/internal/store"
	"golang.org/x/crypto/ssh"
)

func apiGetApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	s := r.Context().Value(
		store.CertRequestContext).(*store.MemoryCertRequestStore)

	cr, err := s.Get(id)
	if err != nil {
		//probably doesn't exit?
		slog.Error("Failed to get request", slog.Any("error", err))
		w.Write([]byte("doesn't exist"))
		return
	}
	_ = s.Delete(id)

	pubKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(cr.Pubkey))

	username := middleware.GetUserName(r)
	groups := middleware.GetGroups(r)

	certOptions := config.GetConfig().CertOptions

	var hoursValid time.Duration
	var certType uint32
	var principals = []string{}
	var ext []string
	var requiredGroup = ""
	extensions := make(map[string]string)
	criticalOptions := make(map[string]string)

	switch cr.Type {
	case "host":
		certType = ssh.HostCert
		requiredGroup = certOptions.Host.RequireGroup
	case "user":
		certType = ssh.UserCert
		ext = certOptions.User.Extensions
	case "service":
		certType = ssh.UserCert
		ext = certOptions.Service.Extensions
		requiredGroup = certOptions.Service.RequireGroup
	}

	if requiredGroup != "" && !slices.Contains(groups, requiredGroup) {
		w.Write([]byte(fmt.Sprintf("Missing required group: %s", requiredGroup)))
		return
	}

	principals = append(principals, username)
	// find all enabled/unlocked generic/aa accounts and add them to principals

	for _, v := range ext {
		extensions[v] = ""
	}

	var serial uint64
	// generate serial for certificate
	if err := binary.Read(rand.Reader, binary.BigEndian, &serial); err != nil {
		slog.Error("unable to read from queue", slog.Any("error", err))
		return
	}

	validAfter := time.Now().Local().Add(hoursValid)
	validBefore := time.Now().Local().Add(certOptions.User.ValidDuration)

	cert := &ssh.Certificate{
		Nonce:           []byte(nonce.NewNonce(32)),
		Key:             pubKey,
		Serial:          serial,
		CertType:        certType,
		KeyId:           username,
		ValidPrincipals: principals, //cr.Principals,
		ValidAfter:      uint64(validAfter.Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
		Permissions: ssh.Permissions{
			Extensions:      extensions,
			CriticalOptions: criticalOptions,
		},
		SignatureKey: pubKey,
	}

	signer, err := ssh.ParsePrivateKey([]byte(config.GetConfig().SshKey))
	if err != nil {
		slog.Error("error loading signer", slog.Any("error", err))
		return
	}
	_ = cert.SignCert(rand.Reader, signer)

	c := store.Certificate{
		ID:          id,
		Certificate: strings.Trim(string(ssh.MarshalAuthorizedKey(cert)), "\n"),
		CreatedAt:   time.Now(),
	}

	certStore := r.Context().Value(
		store.CertificateContext).(*store.MemoryCertificatesStore)

	if err = certStore.Create(&c); err != nil {
		render.Status(r, http.StatusBadRequest)
		_ = render.Render(w, r, api.ErrInvalidRequest(err))
		return
	}

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, &types.ResponseBase{StatusText: "success"})
}
