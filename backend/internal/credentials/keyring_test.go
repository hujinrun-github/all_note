package credentials

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAESGCMRoundTripUsesRandomNonceAndBoundAAD(t *testing.T) {
	keyring, err := NewKeyring("k1", map[string][]byte{"k1": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	aad := testAAD()
	plaintext := []byte("postgres://secret-user:secret-pass@db/private")
	first, err := keyring.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	second, err := keyring.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("encryption must use a fresh random nonce")
	}
	decrypted, err := keyring.Decrypt(first, aad)
	if err != nil || !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round trip failed: %v", err)
	}

	mutations := []func(*AAD){
		func(value *AAD) { value.WorkspaceID = "w2" },
		func(value *AAD) { value.FamilyID = "family-2" },
		func(value *AAD) { value.VersionID = "version-2" },
		func(value *AAD) { value.Kind = "object_s3" },
		func(value *AAD) { value.Version = 2 },
	}
	for _, mutate := range mutations {
		changed := aad
		mutate(&changed)
		if _, err := keyring.Decrypt(first, changed); !errors.Is(err, ErrDecryptFailed) {
			t.Fatalf("AAD substitution error=%v", err)
		}
	}
	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[0] ^= 0xff
	if _, err := keyring.Decrypt(tampered, aad); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("ciphertext tamper error=%v", err)
	}
	for _, err := range []error{
		func() error {
			_, err := keyring.Decrypt(EncryptedSecret{KeyID: "missing", Nonce: first.Nonce, Ciphertext: first.Ciphertext}, aad)
			return err
		}(),
		func() error { _, err := keyring.Decrypt(tampered, aad); return err }(),
	} {
		if strings.Contains(err.Error(), "secret-pass") || strings.Contains(err.Error(), string(plaintext)) {
			t.Fatalf("error leaked plaintext: %v", err)
		}
	}
}

func TestRollingKeyringReadsOldAndRewrapsWithActiveKey(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, 32)
	newKey := bytes.Repeat([]byte{2}, 32)
	oldRing, _ := NewKeyring("old", map[string][]byte{"old": oldKey})
	oldAAD := testAAD()
	oldSecret, err := oldRing.Encrypt([]byte("token-value"), oldAAD)
	if err != nil {
		t.Fatal(err)
	}
	rolling, _ := NewKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	newAAD := oldAAD
	newAAD.VersionID = "version-2"
	newAAD.Version = 2
	rewrapped, err := rolling.Rewrap(oldSecret, oldAAD, newAAD)
	if err != nil {
		t.Fatal(err)
	}
	if rewrapped.KeyID != "new" || bytes.Equal(rewrapped.Nonce, oldSecret.Nonce) {
		t.Fatalf("rewrap did not use active key/new nonce: %+v", rewrapped)
	}
	plaintext, err := rolling.Decrypt(rewrapped, newAAD)
	if err != nil || string(plaintext) != "token-value" {
		t.Fatalf("decrypt rewrapped: %q %v", plaintext, err)
	}
	if err := rolling.ValidateRemoval([]string{"old", "new"}, "old"); !errors.Is(err, ErrKeyStillReferenced) {
		t.Fatalf("referenced old key removal error=%v", err)
	}
	if err := rolling.ValidateRemoval([]string{"new"}, "old"); err != nil {
		t.Fatalf("unreferenced old key removal: %v", err)
	}
	newOnly, _ := NewKeyring("new", map[string][]byte{"new": newKey})
	if _, err := newOnly.Decrypt(oldSecret, oldAAD); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing historical key error=%v", err)
	}
}

func TestLoadKeyringFileValidatesActiveKeySizeAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	validKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	validPath := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(validPath, []byte(`{"version":1,"keys":{"active":"`+validKey+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadKeyringFile(validPath, "active")
	if err != nil || loaded.ActiveKeyID() != "active" {
		t.Fatalf("load valid keyring: %v", err)
	}
	for name, contents := range map[string]string{
		"missing-active": `{"version":1,"keys":{"old":"` + validKey + `"}}`,
		"wrong-size":     `{"version":1,"keys":{"active":"` + base64.StdEncoding.EncodeToString([]byte("short")) + `"}}`,
		"duplicate":      `{"version":1,"keys":{"active":"` + validKey + `","active":"` + validKey + `"}}`,
	} {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadKeyringFile(path, "active"); err == nil {
			t.Fatalf("invalid keyring %s accepted", name)
		}
	}
}

func testAAD() AAD {
	return AAD{Scope: "workspace", WorkspaceID: "w1", FamilyID: "family-1", VersionID: "version-1", Kind: "data_store", Version: 1}
}
