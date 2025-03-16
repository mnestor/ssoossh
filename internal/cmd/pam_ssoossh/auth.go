// Created by Mike Nestor <me@mikenestor.org>
package pam_ssoossh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	api "github.com/mnestor/ssoossh/internal/api/client"
	"github.com/mnestor/ssoossh/internal/ssh"
	cssh "golang.org/x/crypto/ssh"
)

func parseArgs(args []string) map[string]interface{} {
	a := make(map[string]interface{})
	for _, v := range args {
		s := strings.Split(v, "=")
		if len(s) == 2 {
			a[s[0]] = s[1]
		} else {
			a[s[0]] = true
		}
	}
	return a
}

func Authenticate(user string, a []string) (int, error) {
	args := parseArgs(a)
	kp, err := ssh.NewKeyPair(false, true, 2048, "pam", "")
	if err != nil {
		return PAM_AUTH_ERR, err
	}

	server, ok := args["server"]
	if !ok {
		return PAM_AUTHINFO_UNAVAIL, errors.New("not configured correctly in pam.d")
	}

	caFile, ok := args["trusted_ca_file"]
	if !ok {
		return PAM_AUTHINFO_UNAVAIL, errors.New("not configured correctly in pam.d")
	}

	debug := false
	if _, ok = args["debug"]; ok {
		debug = true
	}

	f, err := os.ReadFile(caFile.(string))
	if err != nil {
		return PAM_AUTHINFO_UNAVAIL, errors.New("not configured correctly in pam.d")
	}
	cas := []string{}
	for _, l := range strings.Split(string(f), "\n") {
		if !strings.HasPrefix(l, "#") {
			cas = append(cas, l)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	apiClient := api.NewClient(ctx, server.(string))

	id, err := apiClient.PostPubKey(kp)
	if err != nil {
		return PAM_AUTH_ERR, err
	}

	url := fmt.Sprintf("%s/approve/%s", server, id)

	fmt.Fprintf(os.Stdout, "Please visit the URL to continue!:\n%s\n", url)

	// wait for cert
	var cert string
	if cert, err = apiClient.GetCertificate(id); err != nil {
		return PAM_AUTH_ERR, err
	}

	if cert == "" {
		return PAM_AUTH_ERR, errors.New("empty response")
	}

	if debug {
		fmt.Fprintf(os.Stdout, "Got Cert: %s\n", cert)
	}

	if err = kp.ParseCertificate(cert); err != nil {
		return PAM_AUTH_ERR, err
	}

	certData := kp.GetCertficiate()
	vbTime := time.Unix(int64(certData.ValidBefore), 0)
	signature := strings.Trim(string(cssh.MarshalAuthorizedKey(certData.SignatureKey)), "\n")
	validPrincipal := slices.Contains(certData.ValidPrincipals, user)
	validBefore := time.Now().Before(vbTime)
	validCA := slices.Contains(cas, signature)

	if debug {
		fmt.Fprintf(os.Stdout, "Principals:%t: %s\n", validPrincipal, strings.Join(certData.ValidPrincipals, ", "))
		fmt.Fprintf(os.Stdout, "Signature:%t: %s\n", validCA, signature)
		fmt.Fprintf(os.Stdout, "CA List: \n%s", strings.Join(cas, "\n"))
		fmt.Fprintf(os.Stdout, "ValidBefore:%t: %s\n", validBefore, vbTime.Format(time.RFC1123))
	}

	if validPrincipal && validBefore && validCA {
		return PAM_SUCCESS, nil
	}

	e := fmt.Errorf("valid certificate but invalid for user %s, principals:%t:[%s], before:%t:%s, ca:%t:%s",
		user,
		validPrincipal,
		strings.Join(certData.ValidPrincipals, ","),
		validBefore,
		vbTime.Format(time.RFC1123),
		validCA,
		signature,
	)

	return PAM_CRED_INSUFFICIENT, e
}
