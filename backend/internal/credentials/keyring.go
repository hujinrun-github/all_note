package credentials

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

var (
	ErrKeyNotFound        = errors.New("credential encryption key not found")
	ErrDecryptFailed      = errors.New("credential decryption failed")
	ErrKeyStillReferenced = errors.New("credential key is still referenced")
)

type AAD struct {
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id"`
	FamilyID    string `json:"family_id"`
	VersionID   string `json:"version_id"`
	Kind        string `json:"kind"`
	Version     int64  `json:"version"`
}

type EncryptedSecret struct {
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

type Keyring struct {
	activeKeyID string
	keys        map[string][]byte
	random      io.Reader
}

func NewKeyring(activeKeyID string, keys map[string][]byte) (*Keyring, error) {
	if activeKeyID == "" {
		return nil, errors.New("active credential key id is required")
	}
	copyKeys := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if id == "" {
			return nil, errors.New("credential key id is empty")
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("credential key %q must be 32 bytes", id)
		}
		copyKeys[id] = append([]byte(nil), key...)
	}
	if _, ok := copyKeys[activeKeyID]; !ok {
		return nil, fmt.Errorf("active credential key %q is absent from keyring", activeKeyID)
	}
	return &Keyring{activeKeyID: activeKeyID, keys: copyKeys, random: rand.Reader}, nil
}

func LoadKeyringFile(path, activeKeyID string) (*Keyring, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read credential keyring: %w", err)
	}
	document, err := parseKeyringDocument(contents)
	if err != nil {
		return nil, fmt.Errorf("decode credential keyring: %w", err)
	}
	if document.Version != 1 || len(document.Keys) == 0 {
		return nil, errors.New("credential keyring must use version 1 and contain keys")
	}
	keys := make(map[string][]byte, len(document.Keys))
	for id, encoded := range document.Keys {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode credential key %q: invalid base64", id)
		}
		keys[id] = key
	}
	return NewKeyring(activeKeyID, keys)
}

type keyringDocument struct {
	Version int
	Keys    map[string]string
}

func parseKeyringDocument(contents []byte) (keyringDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return keyringDocument{}, errors.New("keyring root must be an object")
	}
	document := keyringDocument{Keys: make(map[string]string)}
	seenTop := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return keyringDocument{}, err
		}
		field, ok := token.(string)
		if !ok || seenTop[field] {
			return keyringDocument{}, errors.New("duplicate or invalid keyring field")
		}
		seenTop[field] = true
		switch field {
		case "version":
			if err := decoder.Decode(&document.Version); err != nil {
				return keyringDocument{}, err
			}
		case "keys":
			open, err := decoder.Token()
			if err != nil || open != json.Delim('{') {
				return keyringDocument{}, errors.New("keyring keys must be an object")
			}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return keyringDocument{}, err
				}
				id, ok := keyToken.(string)
				if !ok || id == "" {
					return keyringDocument{}, errors.New("credential key id is invalid")
				}
				if _, duplicate := document.Keys[id]; duplicate {
					return keyringDocument{}, fmt.Errorf("duplicate credential key id %q", id)
				}
				var encoded string
				if err := decoder.Decode(&encoded); err != nil {
					return keyringDocument{}, err
				}
				document.Keys[id] = encoded
			}
			if _, err := decoder.Token(); err != nil {
				return keyringDocument{}, err
			}
		default:
			return keyringDocument{}, fmt.Errorf("unknown keyring field %q", field)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return keyringDocument{}, err
	}
	if decoder.More() {
		return keyringDocument{}, errors.New("unexpected keyring content")
	}
	return document, nil
}

func (k *Keyring) Encrypt(plaintext []byte, aad AAD) (EncryptedSecret, error) {
	aadBytes, err := aad.bytes()
	if err != nil {
		return EncryptedSecret{}, err
	}
	gcm, err := k.gcm(k.activeKeyID)
	if err != nil {
		return EncryptedSecret{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(k.random, nonce); err != nil {
		return EncryptedSecret{}, errors.New("generate credential nonce")
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, aadBytes)
	return EncryptedSecret{KeyID: k.activeKeyID, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (k *Keyring) Decrypt(secret EncryptedSecret, aad AAD) ([]byte, error) {
	aadBytes, err := aad.bytes()
	if err != nil {
		return nil, err
	}
	gcm, err := k.gcm(secret.KeyID)
	if err != nil {
		return nil, err
	}
	if len(secret.Nonce) != gcm.NonceSize() {
		return nil, ErrDecryptFailed
	}
	plaintext, err := gcm.Open(nil, secret.Nonce, secret.Ciphertext, aadBytes)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

func (k *Keyring) Rewrap(secret EncryptedSecret, oldAAD, newAAD AAD) (EncryptedSecret, error) {
	plaintext, err := k.Decrypt(secret, oldAAD)
	if err != nil {
		return EncryptedSecret{}, err
	}
	defer clear(plaintext)
	return k.Encrypt(plaintext, newAAD)
}

func (k *Keyring) ValidateRemoval(referencedKeyIDs []string, removing ...string) error {
	remove := make(map[string]struct{}, len(removing))
	for _, id := range removing {
		remove[id] = struct{}{}
	}
	for _, id := range referencedKeyIDs {
		if _, ok := remove[id]; ok {
			return fmt.Errorf("%w: key_id=%s", ErrKeyStillReferenced, id)
		}
	}
	return nil
}

func (k *Keyring) KeyIDs() []string {
	ids := make([]string, 0, len(k.keys))
	for id := range k.keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (k *Keyring) ActiveKeyID() string { return k.activeKeyID }

func (k *Keyring) gcm(keyID string) (cipher.AEAD, error) {
	key, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w: key_id=%s", ErrKeyNotFound, keyID)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("initialize credential cipher")
	}
	return cipher.NewGCM(block)
}

func (aad AAD) bytes() ([]byte, error) {
	if aad.Scope != "system" && aad.Scope != "workspace" {
		return nil, errors.New("credential AAD scope is invalid")
	}
	if aad.Scope == "workspace" && aad.WorkspaceID == "" {
		return nil, errors.New("credential AAD workspace id is required")
	}
	if aad.Scope == "system" && aad.WorkspaceID != "" {
		return nil, errors.New("system credential AAD cannot contain workspace id")
	}
	if aad.FamilyID == "" || aad.VersionID == "" || aad.Kind == "" || aad.Version < 1 {
		return nil, errors.New("credential AAD identity is incomplete")
	}
	return json.Marshal(aad)
}
