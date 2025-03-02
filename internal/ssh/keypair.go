// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"github.com/mnestor/ssoossh/pkg/crypto/keys"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

type KeyPair struct {
	Private    interface{}
	Public     *ssh.PublicKey
	Cert       *ssh.Certificate
	CertString string
	Type       string
	Username   string
}

func NewKeyPair(keyTypeRSA bool, keyTypeEC bool, keySize int, t string) (*KeyPair, error) {
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
		Private:    priv,
		Public:     &sshPub,
		Cert:       nil,
		CertString: "",
		Type:       t,
	}, nil
}

func (k *KeyPair) String() string {
	return string(ssh.MarshalAuthorizedKey(*k.Public))
}

func (k *KeyPair) ParseCertificate(c string) error {
	sshPubkeyCert, _, _, _, err := ssh.ParseAuthorizedKey([]byte(c))
	if err != nil {
		return err
	}

	k.CertString = c
	k.Cert = sshPubkeyCert.(*ssh.Certificate)
	return nil
}
