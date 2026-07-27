package sign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

// Key file names, matching cosign's so an existing cosign.pub drops straight
// in (SPEC.md 11).
const (
	PrivateKeyFile = "cosign.key"
	PublicKeyFile  = "cosign.pub"
)

// PEM block types. PUBLIC KEY / PRIVATE KEY are the PKIX and PKCS#8 spellings
// the standard library reads and writes.
const (
	publicKeyBlock  = "PUBLIC KEY"
	privateKeyBlock = "PRIVATE KEY"
)

// GenerateKey returns a new P-256 signing key.
//
// P-256 over SHA-256 is cosign's default and what the simple-signing and DSSE
// readers below assume. A key of another curve would still sign, and nothing
// in the wire format records which was used, so the choice is fixed here
// rather than offered as an option.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	return key, nil
}

// MarshalPrivateKey renders key as an unencrypted PKCS#8 PEM block.
//
// Not cosign's ENCRYPTED SIGSTORE PRIVATE KEY: that format is scrypt plus NaCl
// secretbox, which is a dependency this package is built to do without (see
// the package doc). The consequence is stated plainly — the file is a private
// key in the clear, and is the caller's to protect.
func MarshalPrivateKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: privateKeyBlock, Bytes: der}), nil
}

// MarshalPublicKey renders the public half as a PKIX PEM block, which is
// byte-for-byte the cosign.pub cosign generate-key-pair writes.
func MarshalPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("encode public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: publicKeyBlock, Bytes: der}), nil
}

// ParsePrivateKey reads a PKCS#8 PEM private key.
func ParsePrivateKey(body []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found; expected a %q block", privateKeyBlock)
	}
	if block.Type != privateKeyBlock {
		// The one mistake worth naming: handing verify or sign the key cosign
		// itself wrote, which this package cannot read.
		return nil, fmt.Errorf("PEM block is %q, want %q "+
			"(cosign's encrypted key format is not supported)", block.Type, privateKeyBlock)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is %T, want an ECDSA key", parsed)
	}
	return key, nil
}

// ParsePublicKey reads a PKIX PEM public key — a cosign.pub, whoever wrote it.
func ParsePublicKey(body []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found; expected a %q block", publicKeyBlock)
	}
	if block.Type != publicKeyBlock {
		return nil, fmt.Errorf("PEM block is %q, want %q", block.Type, publicKeyBlock)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is %T, want an ECDSA key", parsed)
	}
	return pub, nil
}
