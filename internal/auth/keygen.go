// Package auth provides JWK keypair generation for the Bron CLI.
//
// The output format matches `cmd/keygen` in bron-sdk-go: an ES256 (P-256) JWK
// with a 24-char lowercase alphanumeric kid.
package auth

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d,omitempty"`
	Kid string `json:"kid"`
}

type KeyPair struct {
	Public  JWK
	Private JWK
	Kid     string
}

func GenerateKeyPair() (*KeyPair, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ecdh key: %w", err)
	}

	// crypto/ecdh marshals P-256 public keys as 0x04 || X || Y (65 bytes,
	// uncompressed SEC1). Slice off the 0x04 prefix and split into the two
	// 32-byte coordinates — RFC 7518 §6.2.1.2 mandates fixed 32-byte width
	// before base64url, so we never need to pad here.
	pub := priv.PublicKey().Bytes()
	if len(pub) != 65 || pub[0] != 0x04 {
		return nil, fmt.Errorf("unexpected public key encoding: len=%d", len(pub))
	}
	x := base64.RawURLEncoding.EncodeToString(pub[1:33])
	y := base64.RawURLEncoding.EncodeToString(pub[33:65])
	d := base64.RawURLEncoding.EncodeToString(priv.Bytes())

	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate kid: %w", err)
	}
	kid := make([]byte, 24)
	for i, b := range raw {
		kid[i] = charset[int(b)%len(charset)]
	}

	return &KeyPair{
		Public:  JWK{Kty: "EC", Crv: "P-256", X: x, Y: y, Kid: string(kid)},
		Private: JWK{Kty: "EC", Crv: "P-256", X: x, Y: y, D: d, Kid: string(kid)},
		Kid:     string(kid),
	}, nil
}

// MarshalCompact returns the JWK as compact JSON.
func (j JWK) MarshalCompact() (string, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MarshalIndent returns the JWK as pretty-printed JSON.
func (j JWK) MarshalIndent() (string, error) {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
