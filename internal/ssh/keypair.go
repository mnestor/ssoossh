// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"github.com/mnestor/ssoossh/pkg/crypto/keys"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

type KeyPairI interface {
	String() string
	ParseCertificate(string) error
	GetCertficiate() *ssh.Certificate
	GetCertficiateS() string
	GetPrivate() interface{}
	GetUsername() string
	GetCertType() string
}

type KeyPair struct {
	private interface{}
	public  *ssh.PublicKey
	cert    *ssh.Certificate
	// string represenation of certificate
	certString string
	// host, user, service, pam
	certType string
	// uhh?
	username string
}

func NewKeyPair(keyTypeRSA bool, keyTypeEC bool, keySize int, t string, u string) (KeyPairI, error) {
	// Generate keypair
	keyType := keys.DefaultKeyType
	if keyTypeRSA {
		keyType = "RSA"
	} else if keyTypeEC {
		keyType = "EC"
	}

	pub, priv, err := keys.GenerateKeyPair(keyType, keys.DefaultKeyCurve, keySize)
	if err != nil {
		return nil, err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, errors.Wrap(err, "error creating public key")
	}

	return &KeyPair{
		private:    priv,
		public:     &sshPub,
		cert:       nil,
		certString: "",
		certType:   t,
		username:   u,
	}, nil
}

func NewKeyPairForHost(p []byte) KeyPairI {
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(p)
	if err != nil {
		return nil
	}

	return &KeyPair{
		public:   &pubKey,
		certType: "host",
	}
}

func (k *KeyPair) String() string {
	return string(ssh.MarshalAuthorizedKey(*k.public))
}

// Parse a string represenation of a cert into a Certificate and keep the string
func (k *KeyPair) ParseCertificate(c string) error {
	sshPubkeyCert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c))
	if err != nil {
		return err
	}

	k.certString = c
	k.cert = sshPubkeyCert.(*ssh.Certificate)
	return nil
}

func (k *KeyPair) GetCertficiate() *ssh.Certificate {
	return k.cert
}

func (k *KeyPair) GetCertficiateS() string {
	return k.certString
}

func (k *KeyPair) GetPrivate() interface{} {
	return k.private
}

func (k *KeyPair) GetUsername() string {
	return k.username
}

func (k *KeyPair) GetCertType() string {
	return k.certType
}
