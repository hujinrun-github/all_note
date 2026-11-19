package japaneseintegration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
)

func New(cfg Config) (*Service, error) {
	if cfg.ControlDB == nil || cfg.Keyring == nil || cfg.Runtimes == nil || cfg.Authorize == nil {
		return nil, ErrUnavailable
	}
	if cfg.ControlDriver != storage.DriverSQLite && cfg.ControlDriver != storage.DriverPostgres {
		return nil, ErrInvalid
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{cfg: cfg}, nil
}
func randomID() (string, error) {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func bind(driver storage.Driver, s string) string {
	if driver != storage.DriverPostgres {
		return s
	}
	var b strings.Builder
	n := 1
	for _, c := range s {
		if c == '?' {
			fmt.Fprintf(&b, "$%d", n)
			n++
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
func (s *Service) control(query string) string { return bind(s.cfg.ControlDriver, query) }
func (s *Service) authorize(ctx context.Context, workspace, actor string) error {
	if workspace == "" || actor == "" {
		return ErrUnauthorized
	}
	if err := s.cfg.Authorize(ctx, workspace, actor); err != nil {
		return fmt.Errorf("%w: account or membership unavailable", ErrUnauthorized)
	}
	return nil
}
func (s *Service) CreatePairing(ctx context.Context, workspace, actor string) (PairingCode, error) {
	if err := s.authorize(ctx, workspace, actor); err != nil {
		return PairingCode{}, err
	}
	// Fail closed for legacy/fenced tenants before granting integration credentials.
	if _, err := s.cfg.Runtimes.ResolveMobileRuntime(ctx, workspace); err != nil {
		return PairingCode{}, err
	}
	code, err := randomID()
	if err != nil {
		return PairingCode{}, err
	}
	expires := s.cfg.Now().Add(10 * time.Minute)
	_, err = s.cfg.ControlDB.ExecContext(ctx, s.control(`INSERT INTO japanese_integration_pairings(code_hash,workspace_id,actor_id,expires_unix) VALUES(?,?,?,?)`), digest([]byte(code)), workspace, actor, expires.Unix())
	if err != nil {
		return PairingCode{}, err
	}
	return PairingCode{PairingCode: code, ExpiresAt: expires.UTC().Format(time.RFC3339)}, nil
}

const connectionColumns = `id,workspace_id,actor_id,source_instance_id,source_user_id,source_user_name,key_id,state,created_at`

func scanConnection(row interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	e := row.Scan(&c.ID, &c.WorkspaceID, &c.ActorID, &c.SourceInstanceID, &c.SourceUserID, &c.SourceUserName, &c.KeyID, &c.State, &c.CreatedAt)
	return c, e
}
func (s *Service) loadConnection(ctx context.Context, id string) (Connection, error) {
	c, e := scanConnection(s.cfg.ControlDB.QueryRowContext(ctx, s.control(`SELECT `+connectionColumns+` FROM japanese_integration_connections WHERE id=?`), id))
	if errors.Is(e, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, e
}
func connectionAAD(c Connection) credentials.AAD {
	return credentials.AAD{Scope: "workspace", WorkspaceID: c.WorkspaceID, FamilyID: "japanese-integration", VersionID: c.ID, Kind: "hmac", Version: 1}
}
func (s *Service) secret(ctx context.Context, c Connection) (string, error) {
	var raw string
	if e := s.cfg.ControlDB.QueryRowContext(ctx, s.control(`SELECT encrypted_secret FROM japanese_integration_connections WHERE id=?`), c.ID).Scan(&raw); e != nil {
		return "", e
	}
	var encrypted credentials.EncryptedSecret
	if e := json.Unmarshal([]byte(raw), &encrypted); e != nil {
		return "", e
	}
	plain, e := s.cfg.Keyring.Decrypt(encrypted, connectionAAD(c))
	return string(plain), e
}
func (s *Service) Pair(ctx context.Context, input PairRequest) (PairResult, error) {
	if len(input.PairingCode) < 32 || !validID(input.SourceInstanceID) || !validID(input.SourceUserID) || len(input.SourceUserName) > 240 {
		return PairResult{}, ErrInvalid
	}
	// Authorize before reserving a control connection: SQLite control stores
	// deliberately use a single pooled connection, and authorization may query it.
	var authorizedWorkspace, authorizedActor string
	var authorizedExpiry int64
	if err := s.cfg.ControlDB.QueryRowContext(ctx, s.control(`SELECT workspace_id,actor_id,expires_unix FROM japanese_integration_pairings WHERE code_hash=?`), digest([]byte(input.PairingCode))).Scan(&authorizedWorkspace, &authorizedActor, &authorizedExpiry); err != nil || authorizedExpiry < s.cfg.Now().Unix() {
		return PairResult{}, ErrUnauthorized
	}
	if err := s.authorize(ctx, authorizedWorkspace, authorizedActor); err != nil {
		return PairResult{}, err
	}
	tx, e := s.cfg.ControlDB.BeginTx(ctx, nil)
	if e != nil {
		return PairResult{}, e
	}
	defer tx.Rollback()
	var workspace, actor, connectionID string
	var expires int64
	query := `SELECT workspace_id,actor_id,expires_unix,connection_id FROM japanese_integration_pairings WHERE code_hash=?`
	if s.cfg.ControlDriver == storage.DriverPostgres {
		query += ` FOR UPDATE`
	}
	if e = tx.QueryRowContext(ctx, s.control(query), digest([]byte(input.PairingCode))).Scan(&workspace, &actor, &expires, &connectionID); e != nil || expires < s.cfg.Now().Unix() {
		return PairResult{}, ErrUnauthorized
	}
	if workspace != authorizedWorkspace || actor != authorizedActor {
		return PairResult{}, ErrUnauthorized
	}
	var c Connection
	var secret string
	if connectionID != "" {
		c, e = scanConnection(tx.QueryRowContext(ctx, s.control(`SELECT `+connectionColumns+` FROM japanese_integration_connections WHERE id=?`), connectionID))
		if e != nil {
			return PairResult{}, e
		}
		if c.State != "pending" || c.SourceInstanceID != input.SourceInstanceID || c.SourceUserID != input.SourceUserID {
			return PairResult{}, ErrUnauthorized
		}
	} else {
		connectionID, e = randomID()
		if e != nil {
			return PairResult{}, e
		}
		secret, e = randomID()
		if e != nil {
			return PairResult{}, e
		}
		c = Connection{ID: connectionID, WorkspaceID: workspace, ActorID: actor, SourceInstanceID: input.SourceInstanceID, SourceUserID: input.SourceUserID, SourceUserName: input.SourceUserName, KeyID: "v1", State: "pending", CreatedAt: s.cfg.Now().UTC().Format(time.RFC3339Nano)}
		encrypted, encryptErr := s.cfg.Keyring.Encrypt([]byte(secret), connectionAAD(c))
		if encryptErr != nil {
			return PairResult{}, encryptErr
		}
		raw, _ := json.Marshal(encrypted)
		if _, e = tx.ExecContext(ctx, s.control(`INSERT INTO japanese_integration_connections(id,workspace_id,actor_id,source_instance_id,source_user_id,source_user_name,key_id,encrypted_secret,state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`), c.ID, c.WorkspaceID, c.ActorID, c.SourceInstanceID, c.SourceUserID, c.SourceUserName, c.KeyID, string(raw), c.State, c.CreatedAt); e != nil {
			return PairResult{}, e
		}
		result, updateErr := tx.ExecContext(ctx, s.control(`UPDATE japanese_integration_pairings SET connection_id=? WHERE code_hash=? AND connection_id=''`), c.ID, digest([]byte(input.PairingCode)))
		if updateErr != nil {
			return PairResult{}, updateErr
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return PairResult{}, ErrConflict
		}
	}
	if e = tx.Commit(); e != nil {
		return PairResult{}, e
	}
	// Cross-store initialization is resumable; a pending record never authenticates.
	if e = s.initializeConnection(ctx, c); e != nil {
		return PairResult{}, e
	}
	if _, e = s.cfg.ControlDB.ExecContext(ctx, s.control(`UPDATE japanese_integration_connections SET state='active' WHERE id=? AND state='pending'`), c.ID); e != nil {
		return PairResult{}, e
	}
	if secret == "" {
		secret, e = s.secret(ctx, c)
		if e != nil {
			return PairResult{}, e
		}
	}
	return PairResult{ConnectionID: c.ID, KeyID: c.KeyID, Secret: secret, WorkspaceID: c.WorkspaceID}, nil
}
func validID(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 240 && !strings.ContainsAny(s, "\x00\r\n")
}
func (s *Service) active(ctx context.Context, id string) (Connection, error) {
	c, e := s.loadConnection(ctx, id)
	if e != nil {
		return c, e
	}
	if c.State != "active" {
		return c, ErrUnauthorized
	}
	if e = s.authorize(ctx, c.WorkspaceID, c.ActorID); e != nil {
		return c, e
	}
	return c, nil
}
func (s *Service) Authenticate(ctx context.Context, method, target string, headers http.Header, body []byte) (Connection, error) {
	c, e := s.active(ctx, headers.Get(HeaderConnection))
	if e != nil {
		return Connection{}, ErrUnauthorized
	}
	if headers.Get(HeaderKeyID) != c.KeyID {
		return Connection{}, ErrUnauthorized
	}
	secret, e := s.secret(ctx, c)
	if e != nil {
		return Connection{}, ErrUnauthorized
	}
	now := s.cfg.Now()
	if !VerifySignature(headers, method, target, secret, body, now) {
		return Connection{}, ErrUnauthorized
	}
	// Nonce is transport replay prevention. Business event replay uses a fresh nonce.
	if _, e = s.cfg.ControlDB.ExecContext(ctx, s.control(`DELETE FROM japanese_integration_nonces WHERE expires_unix<?`), now.Unix()); e != nil {
		return Connection{}, e
	}
	result, e := s.cfg.ControlDB.ExecContext(ctx, s.control(`INSERT INTO japanese_integration_nonces(connection_id,nonce,expires_unix) VALUES(?,?,?) ON CONFLICT DO NOTHING`), c.ID, headers.Get(HeaderNonce), now.Add(10*time.Minute).Unix())
	if e != nil {
		return Connection{}, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return Connection{}, ErrUnauthorized
	}
	return c, nil
}
func (s *Service) Revoke(ctx context.Context, workspace, actor, id string) error {
	if e := s.authorize(ctx, workspace, actor); e != nil {
		return e
	}
	c, e := s.loadConnection(ctx, id)
	if e != nil {
		return e
	}
	if c.WorkspaceID != workspace {
		return ErrUnauthorized
	}
	// Control is authoritative. Stop new authentication/processing first; the
	// idempotent tenant cleanup repairs a failed second phase on a repeated call.
	if _, e = s.cfg.ControlDB.ExecContext(ctx, s.control(`UPDATE japanese_integration_connections SET state='revoked' WHERE workspace_id=? AND id=?`), workspace, id); e != nil {
		return e
	}
	return s.transaction(ctx, c, false, func(t tenantTx) error {
		_, e := t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_bindings SET enabled=0 WHERE workspace_id=? AND connection_id=?`), workspace, id)
		return e
	})
}
