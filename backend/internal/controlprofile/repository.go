package controlprofile

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/credentials"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type CreateVersionInput struct {
	ID                    string
	FamilyID              string
	WorkspaceID           string
	Kind                  string
	Provider              string
	ConfigJSON            string
	Secret                []byte
	PreserveFromVersionID string
	CreatedBy             string
}

type Version struct {
	ID          string
	FamilyID    string
	WorkspaceID string
	Kind        string
	Version     int64
	Provider    string
	State       string
	ConfigJSON  string
	HasSecret   bool
	KeyID       string
}

func (r *Repository) GetVersion(ctx context.Context, workspaceID, kind, versionID string) (Version, error) {
	var result Version
	var ciphertext []byte
	var keyID sql.NullString
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT id,family_id,workspace_id,kind,version,provider,state,config_json,secret_ciphertext,encryption_key_id FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, versionID).
		Scan(&result.ID, &result.FamilyID, &result.WorkspaceID, &result.Kind, &result.Version, &result.Provider, &result.State, &result.ConfigJSON, &ciphertext, &keyID)
	result.HasSecret = len(ciphertext) > 0
	result.KeyID = keyID.String
	return result, err
}

type ReconcileSystemInput struct {
	CandidateID string
	FamilyID    string
	Kind        string
	Name        string
	Provider    string
	ConfigJSON  string
	Secret      []byte
}

type Repository struct {
	db      *sql.DB
	dialect Dialect
	keyring *credentials.Keyring
}

func New(db *sql.DB, dialect Dialect, keyring *credentials.Keyring) (*Repository, error) {
	if db == nil || keyring == nil {
		return nil, errors.New("profile database and keyring are required")
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, errors.New("unsupported profile repository dialect")
	}
	return &Repository{db: db, dialect: dialect, keyring: keyring}, nil
}

func (r *Repository) CreateFamily(ctx context.Context, workspaceID, familyID, kind, name, actorUserID string) error {
	if workspaceID == "" || familyID == "" || name == "" || actorUserID == "" || !validKind(kind) {
		return errors.New("workspace profile family identity is incomplete")
	}
	_, err := r.db.ExecContext(ctx, r.bind(`INSERT INTO workspace_profile_families(id,workspace_id,kind,name,created_by) VALUES(?,?,?,?,?)`), familyID, workspaceID, kind, name, actorUserID)
	return err
}

func (r *Repository) CreateVersion(ctx context.Context, input CreateVersionInput) (Version, error) {
	if input.ID == "" || input.FamilyID == "" || input.WorkspaceID == "" || input.Provider == "" || input.CreatedBy == "" || !validKind(input.Kind) {
		return Version{}, errors.New("workspace profile version identity is incomplete")
	}
	if len(input.Secret) > 0 && input.PreserveFromVersionID != "" {
		return Version{}, errors.New("new secret and preserve source are mutually exclusive")
	}
	if !json.Valid([]byte(input.ConfigJSON)) {
		return Version{}, errors.New("profile config must be valid JSON")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback()
	var nextVersion int64
	if err := tx.QueryRowContext(ctx, r.bind(`SELECT COALESCE(MAX(version),0)+1 FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND family_id=?`), input.WorkspaceID, input.Kind, input.FamilyID).Scan(&nextVersion); err != nil {
		return Version{}, err
	}
	secret := append([]byte(nil), input.Secret...)
	if input.PreserveFromVersionID != "" {
		var oldCiphertext, oldNonce []byte
		var oldKeyID, oldFamilyID, oldKind string
		var oldVersion int64
		err := tx.QueryRowContext(ctx, r.bind(`SELECT secret_ciphertext,secret_nonce,encryption_key_id,family_id,kind,version FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), input.WorkspaceID, input.Kind, input.PreserveFromVersionID).
			Scan(&oldCiphertext, &oldNonce, &oldKeyID, &oldFamilyID, &oldKind, &oldVersion)
		if err != nil {
			return Version{}, fmt.Errorf("load preserved profile secret: %w", err)
		}
		secret, err = r.keyring.Decrypt(credentials.EncryptedSecret{KeyID: oldKeyID, Nonce: oldNonce, Ciphertext: oldCiphertext}, profileAAD(input.WorkspaceID, oldFamilyID, input.PreserveFromVersionID, oldKind, oldVersion))
		if err != nil {
			return Version{}, err
		}
		defer clear(secret)
	}
	var ciphertext, nonce []byte
	var keyID any
	if len(secret) > 0 {
		encrypted, err := r.keyring.Encrypt(secret, profileAAD(input.WorkspaceID, input.FamilyID, input.ID, input.Kind, nextVersion))
		if err != nil {
			return Version{}, err
		}
		ciphertext, nonce, keyID = encrypted.Ciphertext, encrypted.Nonce, encrypted.KeyID
	}
	_, err = tx.ExecContext(ctx, r.bind(`INSERT INTO workspace_profile_versions(id,family_id,workspace_id,kind,version,provider,state,config_json,secret_ciphertext,secret_nonce,encryption_key_id,created_by) VALUES(?,?,?,?,?,?,'draft',?,?,?,?,?)`),
		input.ID, input.FamilyID, input.WorkspaceID, input.Kind, nextVersion, input.Provider, input.ConfigJSON, nullableBytes(ciphertext), nullableBytes(nonce), keyID, input.CreatedBy)
	if err != nil {
		return Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return Version{}, err
	}
	return Version{ID: input.ID, FamilyID: input.FamilyID, WorkspaceID: input.WorkspaceID, Kind: input.Kind, Version: nextVersion, Provider: input.Provider, State: "draft", ConfigJSON: input.ConfigJSON, HasSecret: len(ciphertext) > 0, KeyID: stringValue(keyID)}, nil
}

func (r *Repository) MarkVerified(ctx context.Context, workspaceID, kind, versionID string) error {
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE workspace_profile_versions SET state='verified',verified_at=CURRENT_TIMESTAMP,last_check_status='ok',last_check_message='' WHERE workspace_id=? AND kind=? AND id=? AND state='draft'`), workspaceID, kind, versionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("profile version verification state conflict")
	}
	return nil
}

func (r *Repository) DecryptSecret(ctx context.Context, workspaceID, kind, versionID string) ([]byte, error) {
	var ciphertext, nonce []byte
	var keyID, familyID string
	var version int64
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT secret_ciphertext,secret_nonce,encryption_key_id,family_id,version FROM workspace_profile_versions WHERE workspace_id=? AND kind=? AND id=?`), workspaceID, kind, versionID).
		Scan(&ciphertext, &nonce, &keyID, &familyID, &version)
	if err != nil {
		return nil, err
	}
	return r.keyring.Decrypt(credentials.EncryptedSecret{KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}, profileAAD(workspaceID, familyID, versionID, kind, version))
}

func (r *Repository) ReconcileSystemCandidate(ctx context.Context, input ReconcileSystemInput) (Version, bool, error) {
	if input.CandidateID == "" || input.FamilyID == "" || input.Name == "" || input.Provider == "" || !validKind(input.Kind) || !json.Valid([]byte(input.ConfigJSON)) {
		return Version{}, false, errors.New("system profile candidate is incomplete")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(input.ConfigJSON)); err != nil {
		return Version{}, false, err
	}
	configJSON := compact.String()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, false, err
	}
	defer tx.Rollback()
	var familyCount int
	if err := tx.QueryRowContext(ctx, r.bind(`SELECT COUNT(*) FROM system_profile_families WHERE kind=? AND id=?`), input.Kind, input.FamilyID).Scan(&familyCount); err != nil {
		return Version{}, false, err
	}
	if familyCount == 0 {
		if _, err := tx.ExecContext(ctx, r.bind(`INSERT INTO system_profile_families(id,kind,name) VALUES(?,?,?)`), input.FamilyID, input.Kind, input.Name); err != nil {
			return Version{}, false, err
		}
	}
	var latestID, latestProvider, latestConfig, latestState string
	var latestVersion int64
	var latestCiphertext, latestNonce []byte
	var latestKeyID sql.NullString
	err = tx.QueryRowContext(ctx, r.bind(`SELECT id,version,provider,state,config_json,secret_ciphertext,secret_nonce,encryption_key_id FROM system_profile_versions WHERE kind=? AND family_id=? ORDER BY version DESC LIMIT 1`), input.Kind, input.FamilyID).
		Scan(&latestID, &latestVersion, &latestProvider, &latestState, &latestConfig, &latestCiphertext, &latestNonce, &latestKeyID)
	if err == nil {
		sameSecret := len(input.Secret) == 0 && len(latestCiphertext) == 0
		if len(latestCiphertext) > 0 && latestKeyID.Valid {
			plain, decryptErr := r.keyring.Decrypt(credentials.EncryptedSecret{KeyID: latestKeyID.String, Nonce: latestNonce, Ciphertext: latestCiphertext}, systemProfileAAD(input.FamilyID, latestID, input.Kind, latestVersion))
			if decryptErr != nil {
				return Version{}, false, decryptErr
			}
			sameSecret = subtle.ConstantTimeCompare(plain, input.Secret) == 1
			clear(plain)
		}
		if latestProvider == input.Provider && latestConfig == configJSON && sameSecret {
			if err := tx.Commit(); err != nil {
				return Version{}, false, err
			}
			return Version{ID: latestID, FamilyID: input.FamilyID, Kind: input.Kind, Version: latestVersion, Provider: latestProvider, State: latestState, ConfigJSON: latestConfig, HasSecret: len(latestCiphertext) > 0, KeyID: latestKeyID.String}, false, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Version{}, false, err
	}
	nextVersion := latestVersion + 1
	var ciphertext, nonce []byte
	var keyID any
	if len(input.Secret) > 0 {
		encrypted, err := r.keyring.Encrypt(input.Secret, systemProfileAAD(input.FamilyID, input.CandidateID, input.Kind, nextVersion))
		if err != nil {
			return Version{}, false, err
		}
		ciphertext, nonce, keyID = encrypted.Ciphertext, encrypted.Nonce, encrypted.KeyID
	}
	if _, err := tx.ExecContext(ctx, r.bind(`INSERT INTO system_profile_versions(id,family_id,kind,version,provider,state,config_json,secret_ciphertext,secret_nonce,encryption_key_id) VALUES(?,?,?,?,?,'draft',?,?,?,?)`), input.CandidateID, input.FamilyID, input.Kind, nextVersion, input.Provider, configJSON, nullableBytes(ciphertext), nullableBytes(nonce), keyID); err != nil {
		return Version{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Version{}, false, err
	}
	return Version{ID: input.CandidateID, FamilyID: input.FamilyID, Kind: input.Kind, Version: nextVersion, Provider: input.Provider, State: "draft", ConfigJSON: configJSON, HasSecret: len(ciphertext) > 0, KeyID: stringValue(keyID)}, true, nil
}

func (r *Repository) MarkSystemVerified(ctx context.Context, kind, versionID string) error {
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE system_profile_versions SET state='verified',verified_at=CURRENT_TIMESTAMP,last_check_status='ok',last_check_message='' WHERE kind=? AND id=? AND state='draft'`), kind, versionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("system profile verification state conflict")
	}
	return nil
}

func profileAAD(workspaceID, familyID, versionID, kind string, version int64) credentials.AAD {
	return credentials.AAD{Scope: "workspace", WorkspaceID: workspaceID, FamilyID: familyID, VersionID: versionID, Kind: kind, Version: version}
}

func systemProfileAAD(familyID, versionID, kind string, version int64) credentials.AAD {
	return credentials.AAD{Scope: "system", FamilyID: familyID, VersionID: versionID, Kind: kind, Version: version}
}

func validKind(kind string) bool {
	return kind == "data_store" || kind == "object_s3" || kind == "llm_chat" || kind == "llm_transcription"
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return value.(string)
}

func (r *Repository) bind(query string) string {
	if r.dialect == DialectSQLite {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
