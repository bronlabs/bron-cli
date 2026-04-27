// Package auth provides JWK keypair generation for the Bron CLI.
//
// The output format matches `cmd/keygen` in bron-sdk-go: an ES256 (P-256) JWK
// with a 24-char lowercase alphanumeric kid.
package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
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
	priv, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ecdsa key: %w", err)
	}

	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	kid := make([]byte, 24)
	for i := range kid {
		kid[i] = charset[rand.Intn(len(charset))]
	}

	x := base64.RawURLEncoding.EncodeToString(priv.PublicKey.X.Bytes())
	y := base64.RawURLEncoding.EncodeToString(priv.PublicKey.Y.Bytes())

	return &KeyPair{
		Public:  JWK{Kty: "EC", Crv: "P-256", X: x, Y: y, Kid: string(kid)},
		Private: JWK{Kty: "EC", Crv: "P-256", X: x, Y: y, D: base64.RawURLEncoding.EncodeToString(priv.D.Bytes()), Kid: string(kid)},
		Kid:     string(kid),
	}, nil
}

// MarshalJSON returns the JWK as compact JSON.
func (j JWK) MarshalCompact() (string, error) {
	b, err := json.Marshal(j)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MarshalJSONIndent returns the JWK as pretty-printed JSON.
func (j JWK) MarshalIndent() (string, error) {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
