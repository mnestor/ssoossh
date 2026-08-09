package main

import (
	"errors"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

func Authenticate(log *Logger, user string, cfg config) (int, error) {
	kp, err := keypair.NewSSHKeypair("ed25519", 0)
	if err != nil {
		return PamAbort, err
	}

	if cfg.server == "" {
		return PamUserUnknown, errors.New("not configured correctly in pam.d")
	}

	if cfg.trustedCAFile == "" {
		return PamNoModuleData, errors.New("not configured correctly in pam.d")
	}

	//TODO: read cfg.trustedCAFile and split contents into []string
	// cas := strings.Split(cfg.trustedCAFile, "\n")

	// ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	// defer cancel()

	// apiClient, err := api.NewClient(api.Config{
	// 	ServerURL:     cfg.server,
	// 	SkipVerifySSL: cfg.insecureSkipVerify,
	// })

	// id, err := apiClient.PostPubKey(kp)
	// if err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	// url := fmt.Sprintf("%s/approve/%s", cfg.server, id)

	// fmt.Fprintf(os.Stdout, "Please visit the URL to continue!:\n%s\n", url)

	// // wait for cert
	// var cert string
	// if cert, err = apiClient.GetCertificate(id); err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	var cert = ""
	if cert == "" {
		return PamAuthInfoUnavail, errors.New("empty response")
	}

	// if cfg.debug == "stdout" {
	// 	fmt.Fprintf(os.Stdout, "Got Cert: %s\n", cert)
	// }

	// if err = kp.ParseCertificate(cert); err != nil {
	// 	return PAM_AUTH_ERR, err
	// }

	// certData := kp.GetCertficiate()
	// vbTime := time.Unix(int64(certData.ValidBefore), 0)
	// signature := strings.Trim(string(cssh.MarshalAuthorizedKey(certData.SignatureKey)), "\n")
	// validPrincipal := slices.Contains(certData.ValidPrincipals, user)
	// validBefore := time.Now().Before(vbTime)
	// validCA := slices.Contains(cas, signature)

	// if cfg.debug == "stdout" {
	// 	fmt.Fprintf(os.Stdout, "Principals:%t: %s\n", validPrincipal, strings.Join(certData.ValidPrincipals, ", "))
	// 	fmt.Fprintf(os.Stdout, "Signature:%t: %s\n", validCA, signature)
	// 	fmt.Fprintf(os.Stdout, "CA List: \n%s", strings.Join(cas, "\n"))
	// 	fmt.Fprintf(os.Stdout, "ValidBefore:%t: %s\n", validBefore, vbTime.Format(time.RFC1123))
	// }

	// if validPrincipal && validBefore && validCA {
	// 	return PAM_SUCCESS, nil
	// }

	// e := fmt.Errorf("valid certificate but invalid for user %s, principals:%t:[%s], before:%t:%s, ca:%t:%s",
	// 	user,
	// 	validPrincipal,
	// 	strings.Join(certData.ValidPrincipals, ","),
	// 	validBefore,
	// 	vbTime.Format(time.RFC1123),
	// 	validCA,
	// 	signature,
	// )

	return PamCredInsufficient, nil //e
}
