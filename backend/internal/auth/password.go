package auth

import (
	"errors"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

var ErrWeakPassword = errors.New("weak password")

const (
	PasswordBcryptCost = 12
	PasswordMaxBytes   = 72
)

func HashPassword(password string) (string, error) {
	if err := ValidatePasswordPolicy(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), PasswordBcryptCost)
	return string(hash), err
}

func VerifyPassword(hash string, password string) error {
	if len([]byte(password)) > PasswordMaxBytes {
		return ErrWeakPassword
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func ValidatePasswordPolicy(password string) error {
	if len([]byte(password)) > PasswordMaxBytes {
		return ErrWeakPassword
	}
	if len([]rune(password)) < 8 {
		return ErrWeakPassword
	}
	hasLetter := false
	hasDigit := false
	for _, r := range password {
		if unicode.IsLetter(r) {
			hasLetter = true
		}
		if unicode.IsDigit(r) {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return ErrWeakPassword
	}
	return nil
}
