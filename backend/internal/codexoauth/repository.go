package codexoauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/credentials"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type Flow struct {
	ID, WorkspaceID, UserID, DeviceAuthID, UserCode, VerificationURL, State string
	Interval                                                                time.Duration
	ExpiresAt                                                               time.Time
}
type Repository struct {
	db      *sql.DB
	dialect Dialect
	keyring *credentials.Keyring
}

func NewRepository(db *sql.DB, dialect Dialect, keyring *credentials.Keyring) (*Repository, error) {
	if db == nil || keyring == nil {
		return nil, errors.New("Codex OAuth repository dependencies are required")
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, errors.New("unsupported Codex OAuth dialect")
	}
	return &Repository{db: db, dialect: dialect, keyring: keyring}, nil
}

func (r *Repository) Create(ctx context.Context, flow Flow) error {
	plain, _ := json.Marshal(map[string]string{"device_auth_id": flow.DeviceAuthID})
	encrypted, err := r.keyring.Encrypt(plain, r.aad(flow))
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.bind(`INSERT INTO codex_oauth_device_flows(id,workspace_id,user_id,device_ciphertext,device_nonce,encryption_key_id,user_code,verification_url,poll_interval_seconds,expires_at_unix,state) VALUES(?,?,?,?,?,?,?,?,?,?, 'pending')`), flow.ID, flow.WorkspaceID, flow.UserID, encrypted.Ciphertext, encrypted.Nonce, encrypted.KeyID, flow.UserCode, flow.VerificationURL, int64(flow.Interval/time.Second), flow.ExpiresAt.Unix())
	return err
}
func (r *Repository) Get(ctx context.Context, id, workspaceID, userID string) (Flow, error) {
	var f Flow
	var cipher, nonce []byte
	var key string
	var interval, expires int64
	err := r.db.QueryRowContext(ctx, r.bind(`SELECT id,workspace_id,user_id,device_ciphertext,device_nonce,encryption_key_id,user_code,verification_url,poll_interval_seconds,expires_at_unix,state FROM codex_oauth_device_flows WHERE id=? AND workspace_id=? AND user_id=?`), id, workspaceID, userID).Scan(&f.ID, &f.WorkspaceID, &f.UserID, &cipher, &nonce, &key, &f.UserCode, &f.VerificationURL, &interval, &expires, &f.State)
	if err != nil {
		return Flow{}, err
	}
	plain, err := r.keyring.Decrypt(credentials.EncryptedSecret{KeyID: key, Nonce: nonce, Ciphertext: cipher}, r.aad(f))
	if err != nil {
		return Flow{}, err
	}
	defer clear(plain)
	var secret struct {
		DeviceAuthID string `json:"device_auth_id"`
	}
	if json.Unmarshal(plain, &secret) != nil || secret.DeviceAuthID == "" {
		return Flow{}, errors.New("invalid encrypted Codex device flow")
	}
	f.DeviceAuthID = secret.DeviceAuthID
	f.Interval = time.Duration(interval) * time.Second
	f.ExpiresAt = time.Unix(expires, 0)
	return f, nil
}
func (r *Repository) Complete(ctx context.Context, id, workspaceID, userID, state string) error {
	if state != "authorized" && state != "expired" && state != "failed" {
		return errors.New("invalid Codex device flow terminal state")
	}
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE codex_oauth_device_flows SET state=? WHERE id=? AND workspace_id=? AND user_id=? AND state='pending'`), state, id, workspaceID, userID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return errors.New("Codex device flow state conflict")
	}
	return nil
}
func (r *Repository) aad(f Flow) credentials.AAD {
	return credentials.AAD{Scope: "workspace", WorkspaceID: f.WorkspaceID, FamilyID: "codex-oauth-device-flow", VersionID: f.ID, Kind: "llm_chat", Version: 1}
}
func (r *Repository) bind(query string) string {
	if r.dialect == DialectSQLite {
		return query
	}
	var b strings.Builder
	n := 1
	for _, ch := range query {
		if ch == '?' {
			fmt.Fprintf(&b, "$%d", n)
			n++
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}
