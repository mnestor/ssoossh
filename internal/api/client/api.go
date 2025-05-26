// Created By Mike Nestor <me@mikenestor.org>
package api

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"

	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
	"github.com/mnestor/ssoossh/internal/version"

	"resty.dev/v3"
)

type ClientI interface {
	GetCA() (string, error)
	GetCertificate(string) (string, error)
	PostPubKey(string, string, string) (string, error)
}

type Client struct {
	// ClientI
	*resty.Request
}

func NewClient(ctx context.Context, s string) ClientI {
	cfg := ctx.Value(sc.ContextKeyConfig).(config.Config)
	client := resty.New().
		SetContext(ctx).
		SetBaseURL(fmt.Sprintf("%s/api/%s/", strings.Trim(s, "/"), version.ApiPath)).
		SetHeader("Accept", "application/json").
		SetTLSClientConfig(&tls.Config{
			InsecureSkipVerify: !cfg.VerifySsl,
			VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
				if len(cfg.SslFingerprint) > 0 {
					s := sha256.Sum256(rawCerts[0])
					b := strings.ToUpper(hex.EncodeToString(s[:]))
					if b != strings.ReplaceAll(cfg.SslFingerprint, ":", "") {
						// Insert a ':' every 2nd character
						var bWithColons strings.Builder
						for i, c := range b {
							if i > 0 && i%2 == 0 {
								bWithColons.WriteByte(':')
							}
							bWithColons.WriteByte(byte(c))
						}
						bColon := bWithColons.String()
						e := fmt.Errorf("fingerprint mismatch.\nexpected: %s\ngot:      %s", cfg.SslFingerprint, bColon) // x509.VerifyHostname(rawCerts, cfg.Server)
						fmt.Fprintln(os.Stderr, e.Error())
						return e
					}
				}
				if cfg.VerifyServerName {
					cert, err := x509.ParseCertificate(rawCerts[0])
					if err != nil {
						fmt.Fprintf(os.Stderr, "failed to parse certificate: %s\n", err.Error())
						return err
					}

					serverName, err := url.Parse(cfg.Server)
					if err != nil || !slices.Contains(cert.DNSNames, serverName.Hostname()) {
						fmt.Fprintf(os.Stderr, "server name verification failed: %s not present in certificate\n", serverName.Hostname())
						return err
					}
				}
				return nil
			},
		})

	return &Client{
		Request: client.
			R(). // this passes a contextWithoutCancel so set it with our cancel
			SetContext(ctx),
	}
}
