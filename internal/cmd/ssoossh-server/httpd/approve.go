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
	types "github.com/mnestor/ssoossh/internal/api/response_types"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/api/v1"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh-server/httpd/middleware"
	"github.com/mnestor/ssoossh/internal/config"
	"github.com/mnestor/ssoossh/internal/nonce"
	"github.com/mnestor/ssoossh/internal/store"
	"golang.org/x/crypto/ssh"
)

var getApproveConfig = config.GetConfig

func apiGetApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	reqStore := r.Context().Value(store.CertRequestContext).(store.CertRequestInterface)
	certStore := r.Context().Value(store.CertificateContext).(store.CertificateInterface)
	auditStore := r.Context().Value(store.AuditLogContext).(store.AuditLogInterface)

	cr, err := reqStore.Get(id)
	if err != nil {
		slog.Error("Failed to get request", slog.Any("error", err))
		_, _ = w.Write([]byte("doesn't exist"))
		return
	}
	_ = reqStore.Delete(id)

	pubKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(cr.Pubkey))

	username := middleware.GetUserName(r)
	groups := middleware.GetGroups(r)

	certOptions := getApproveConfig().CertOptions

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
	case "pam":
		certType = ssh.UserCert
		principals = append(principals, username)
	case "user":
		certType = ssh.UserCert
		ext = certOptions.User.Extensions
		principals = append(principals, username)
	case "service":
		certType = ssh.UserCert
		ext = certOptions.Service.Extensions
		requiredGroup = certOptions.Service.RequireGroup
		principals = append(principals, cr.Account)
	}

	if requiredGroup != "" && !slices.Contains(groups, requiredGroup) {
		_, _ = w.Write([]byte(fmt.Sprintf("Missing required group: %s", requiredGroup)))
		return
	}

	for _, v := range ext {
		extensions[v] = ""
	}

	var serial uint64
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
		KeyId:           fmt.Sprintf("%s@%s", username, r.RemoteAddr),
		ValidPrincipals: principals,
		ValidAfter:      uint64(validAfter.Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
		Permissions: ssh.Permissions{
			Extensions:      extensions,
			CriticalOptions: criticalOptions,
		},
		SignatureKey: pubKey,
	}

	signer, err := ssh.ParsePrivateKey([]byte(getApproveConfig().SshKey))
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

	if err = certStore.Create(&c); err != nil {
		render.Status(r, http.StatusBadRequest)
		_ = render.Render(w, r, api.ErrInvalidRequest(err))
		return
	}

	_ = auditStore.Create(&store.AuditLogEntry{
		RequestID: id,
		UserName:  username,
		Decision:  "approved",
		PublicKey: cr.Pubkey,
		Account:   cr.Account,
		CertType:  cr.Type,
	})

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, &types.ResponseBase{StatusText: "success"})
}

func apiGetReject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	reqStore := r.Context().Value(store.CertRequestContext).(store.CertRequestInterface)
	certStore := r.Context().Value(store.CertificateContext).(store.CertificateInterface)
	auditStore := r.Context().Value(store.AuditLogContext).(store.AuditLogInterface)

	cr, err := reqStore.Get(id)
	if err != nil {
		slog.Error("Failed to get request for rejection", slog.Any("error", err))
		_, _ = w.Write([]byte("doesn't exist"))
		return
	}
	_ = reqStore.Delete(id)

	username := middleware.GetUserName(r)

	_ = certStore.Reject(id)

	_ = auditStore.Create(&store.AuditLogEntry{
		RequestID: id,
		UserName:  username,
		Decision:  "rejected",
		PublicKey: cr.Pubkey,
		Account:   cr.Account,
		CertType:  cr.Type,
	})

	render.Status(r, http.StatusOK)
	_ = render.Render(w, r, &types.ResponseBase{StatusText: "rejected"})
}
