package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if hash == "secret123" {
		t.Fatal("hash must not equal plaintext")
	}
	if err := VerifyPassword(hash, "secret123"); err != nil {
		t.Fatalf("verify valid password: %v", err)
	}
	if err := VerifyPassword(hash, "wrong123"); err == nil {
		t.Fatal("expected invalid password error")
	}
}

func TestHashPasswordUsesCost12(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("read bcrypt cost: %v", err)
	}
	if cost != PasswordBcryptCost {
		t.Fatalf("bcrypt cost = %d, want %d", cost, PasswordBcryptCost)
	}
}

func TestHashPasswordRejectsPasswordsOverBcryptLimit(t *testing.T) {
	password := "a1" + strings.Repeat("a", PasswordMaxBytes-1)

	if len([]byte(password)) != PasswordMaxBytes+1 {
		t.Fatalf("password byte length = %d, want %d", len([]byte(password)), PasswordMaxBytes+1)
	}
	if err := ValidatePasswordPolicy(password); err == nil {
		t.Fatal("expected overlong password to fail policy")
	}
	if _, err := HashPassword(password); err == nil {
		t.Fatal("expected overlong password to fail before hashing")
	}
}

func TestVerifyPasswordDocumentsBcrypt72ByteBoundary(t *testing.T) {
	password := "a1" + strings.Repeat("a", PasswordMaxBytes-2)
	alteredPastLimit := password + "changed-suffix"

	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordBcryptCost)
	if err != nil {
		t.Fatalf("generate raw bcrypt hash: %v", err)
	}
	if err := VerifyPassword(string(hash), alteredPastLimit); err == nil {
		t.Fatal("expected password with altered suffix past 72 bytes to be rejected")
	}
}

func TestValidatePasswordPolicy(t *testing.T) {
	if err := ValidatePasswordPolicy("abc12345"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := ValidatePasswordPolicy("abcdefghi"); err == nil {
		t.Fatal("expected password without digit to fail")
	}
	if err := ValidatePasswordPolicy("123456789"); err == nil {
		t.Fatal("expected password without letter to fail")
	}
	if err := ValidatePasswordPolicy("a1"); err == nil {
		t.Fatal("expected short password to fail")
	}
}
