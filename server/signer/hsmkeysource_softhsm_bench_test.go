//go:build hsm && softhsm

package signer

// Signing cost per CA key type, for the table in
// https://mnestor.github.io/ssoossh/operations/hsm/.
//
// One iteration is one ssh.Certificate.SignCert: exactly the work the
// signer does per issued certificate, and the only per-request cost that
// depends on the CA key type.
//
// These run against SoftHSM2, which is a software emulator. The numbers
// therefore measure the algorithm, not a token: a real HSM adds command
// and transport latency that usually dominates everything below. Their
// value is the ratio between key types and the comparison against the
// in-process ssh_key path, both of which do carry over.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"testing"

	"golang.org/x/crypto/ssh"
)

// hsmBenchKeys are the CA key types the HSM path accepts, named as
// pkcs11-tool's --key-type wants them.
var hsmBenchKeys = []struct {
	name    string
	keyType string
	// token and key labels must differ per case: SoftHSM2 assigns slot IDs
	// at random, so provisionToken pins each key to its own token label.
	token string
	label string
	id    string
}{
	{name: "ECDSA-P256", keyType: "EC:prime256v1", token: "bench-ec256", label: "ec256", id: "11"},
	{name: "ECDSA-P384", keyType: "EC:secp384r1", token: "bench-ec384", label: "ec384", id: "12"},
	{name: "ECDSA-P521", keyType: "EC:secp521r1", token: "bench-ec521", label: "ec521", id: "13"},
	{name: "RSA-2048", keyType: "RSA:2048", token: "bench-rsa2048", label: "rsa2048", id: "14"},
	{name: "RSA-3072", keyType: "RSA:3072", token: "bench-rsa3072", label: "rsa3072", id: "15"},
	{name: "RSA-4096", keyType: "RSA:4096", token: "bench-rsa4096", label: "rsa4096", id: "16"},
}

// BenchmarkHSMSignCert measures one certificate signature per iteration
// with the CA key on a SoftHSM2 token.
func BenchmarkHSMSignCert(b *testing.B) {
	pubStr, err := newTestPublicKeyString(b)
	if err != nil {
		b.Fatalf("failed to generate test public key: %v", err)
	}

	for _, tc := range hsmBenchKeys {
		b.Run(tc.name, func(b *testing.B) {
			module := provisionToken(b, tc.token, tc.keyType, tc.label, tc.id)

			hs, err := NewHSMKeySource(HSMParams{
				Module:     module,
				TokenLabel: tc.token,
				PIN:        "1234",
				KeyLabel:   tc.label,
			})
			if err != nil {
				b.Fatalf("failed to create HSM key source: %v", err)
			}
			defer hs.Close()

			signer, err := hs.Signer(context.Background())
			if err != nil {
				b.Fatalf("failed to get signer: %v", err)
			}

			// Built once, outside the timer: SignCert regenerates the
			// nonce and re-marshals the body on every call, so signing
			// the same struct repeatedly is the same work, and keeping
			// construction out of the loop stops authorized-key parsing
			// from swamping the fast curves.
			cert := buildTestCert(b, pubStr)

			b.ResetTimer()
			for range b.N {
				if err := cert.SignCert(rand.Reader, signer); err != nil {
					b.Fatalf("SignCert: %v", err)
				}
			}
		})
	}
}

// BenchmarkHSMSignJob measures a whole signing job -- Sign(), which is
// what the handler actually calls per message: lifetime validation, public
// key parsing, certificate construction, the signature, and marshalling
// the reply.
//
// It exists to keep BenchmarkHSMSignCert honest. The bare signature is a
// floor, not a throughput figure: for the fast curves the work around it
// costs more than the signature does, so quoting 1/SignCert as
// certificates per second overstates a signer badly.
func BenchmarkHSMSignJob(b *testing.B) {
	job := newTestJob(b)
	limits := newDefaultTestLimits()
	ctx := context.Background()

	for _, tc := range hsmBenchKeys {
		b.Run(tc.name, func(b *testing.B) {
			module := provisionToken(b, tc.token+"-job", tc.keyType, tc.label, tc.id)

			hs, err := NewHSMKeySource(HSMParams{
				Module:     module,
				TokenLabel: tc.token + "-job",
				PIN:        "1234",
				KeyLabel:   tc.label,
			})
			if err != nil {
				b.Fatalf("failed to create HSM key source: %v", err)
			}
			defer hs.Close()

			b.ResetTimer()
			for range b.N {
				if _, err := Sign(ctx, hs, job, false, limits); err != nil {
					b.Fatalf("Sign: %v", err)
				}
			}
		})
	}
}

// BenchmarkInProcessSignCert is the ssh_key comparison: the same signature
// with the CA key in process memory, so the HSM figures above can be read
// as a cost over this baseline rather than in isolation.
func BenchmarkInProcessSignCert(b *testing.B) {
	pubStr, err := newTestPublicKeyString(b)
	if err != nil {
		b.Fatalf("failed to generate test public key: %v", err)
	}

	cases := []struct {
		name string
		gen  func(b *testing.B) ssh.Signer
	}{
		{"ECDSA-P256", func(b *testing.B) ssh.Signer { return ecdsaSigner(b, elliptic.P256()) }},
		{"ECDSA-P384", func(b *testing.B) ssh.Signer { return ecdsaSigner(b, elliptic.P384()) }},
		{"ECDSA-P521", func(b *testing.B) ssh.Signer { return ecdsaSigner(b, elliptic.P521()) }},
		{"RSA-2048", func(b *testing.B) ssh.Signer { return rsaSigner(b, 2048) }},
		{"RSA-3072", func(b *testing.B) ssh.Signer { return rsaSigner(b, 3072) }},
		{"RSA-4096", func(b *testing.B) ssh.Signer { return rsaSigner(b, 4096) }},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			signer := tc.gen(b)
			cert := buildTestCert(b, pubStr)

			b.ResetTimer()
			for range b.N {
				if err := cert.SignCert(rand.Reader, signer); err != nil {
					b.Fatalf("SignCert: %v", err)
				}
			}
		})
	}
}

// ecdsaSigner builds an in-process ECDSA CA signer on curve c.
func ecdsaSigner(b *testing.B, c elliptic.Curve) ssh.Signer {
	b.Helper()
	key, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		b.Fatalf("generate ECDSA key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		b.Fatalf("wrap ECDSA key: %v", err)
	}
	return s
}

// rsaSigner builds an in-process RSA CA signer, restricted to the same
// SHA-2 algorithms wrapCASigner allows on the HSM path so the two columns
// compare like with like.
func rsaSigner(b *testing.B, bits int) ssh.Signer {
	b.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		b.Fatalf("generate RSA key: %v", err)
	}
	s, err := ssh.NewSignerFromKey(key)
	if err != nil {
		b.Fatalf("wrap RSA key: %v", err)
	}
	as, ok := s.(ssh.AlgorithmSigner)
	if !ok {
		b.Fatal("RSA signer does not support algorithm selection")
	}
	ms, err := ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256})
	if err != nil {
		b.Fatalf("restrict RSA algorithms: %v", err)
	}
	return ms
}
