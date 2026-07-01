package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrMissingSessionSecret = errors.New("missing session secret")
	ErrMissingSessionToken  = errors.New("missing session token")
)

func GenerateSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashSessionToken(secret string, token string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrMissingSessionSecret
	}
	if strings.TrimSpace(token) == "" {
		return "", ErrMissingSessionToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
