package auth

import (
	"regexp"
	"testing"
)

func TestSessionTokenHashUsesSecretPepper(t *testing.T) {
	secret := "test-session-secret-with-at-least-32-bytes"
	token := "session-token-value"
	hash1, err := HashSessionToken(secret, token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	hash2, err := HashSessionToken(secret, token)
	if err != nil {
		t.Fatalf("hash token again: %v", err)
	}
	if hash1 != hash2 {
		t.Fatal("hash must be deterministic")
	}
	if hash1 == token {
		t.Fatal("hash must not equal token")
	}
	otherHash, err := HashSessionToken("other-session-secret-with-at-least-32-bytes", token)
	if err != nil {
		t.Fatalf("hash with other secret: %v", err)
	}
	if otherHash == hash1 {
		t.Fatal("hash must change when secret changes")
	}
}

func TestSessionTokenHashRequiresSecret(t *testing.T) {
	if _, err := HashSessionToken("", "session-token-value"); err == nil {
		t.Fatal("expected missing session secret error")
	}
}

func TestSessionTokenHashRequiresToken(t *testing.T) {
	if _, err := HashSessionToken("test-session-secret-with-at-least-32-bytes", " "); err == nil {
		t.Fatal("expected missing session token error")
	}
}

func TestSessionTokenHashUsesHMACSHA256(t *testing.T) {
	secret := "test-session-secret-with-at-least-32-bytes"
	token := "session-token-value"
	const want = "998ed80c56c7a1873fed66274a64f852baf61948111e70247cab6004215d72eb"

	got, err := HashSessionToken(secret, token)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}
	if got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

func TestGenerateSessionToken(t *testing.T) {
	token, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if len(token) != 43 {
		t.Fatalf("token length = %d, want 43", len(token))
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(token) {
		t.Fatalf("token contains non URL-safe raw base64 characters: %q", token)
	}
	if regexp.MustCompile(`=+$`).MatchString(token) {
		t.Fatalf("token should not include base64 padding: %q", token)
	}
	otherToken, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("generate second token: %v", err)
	}
	if otherToken == token {
		t.Fatal("expected generated session tokens to be unique")
	}
}
