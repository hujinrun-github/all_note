package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/storage"
)

func (p Provider) ExportTenantSnapshot(ctx context.Context, cfg storage.Config, workspaceID string) (storage.TenantSnapshot, error) {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return storage.TenantSnapshot{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return storage.TenantSnapshot{}, err
	}
	defer tx.Rollback()
	var state, installationID string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM tenant_workspaces WHERE workspace_id=?`, workspaceID).Scan(&state); err != nil {
		return storage.TenantSnapshot{}, err
	}
	if state != "fenced" {
		return storage.TenantSnapshot{}, fmt.Errorf("workspace %s must be fenced before snapshot", workspaceID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT installation_id FROM tenant_installations WHERE singleton_key=1`).Scan(&installationID); err != nil {
		return storage.TenantSnapshot{}, err
	}
	var schemaVersion string
	if err := tx.QueryRowContext(ctx, `SELECT version FROM tenant_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&schemaVersion); err != nil {
		return storage.TenantSnapshot{}, err
	}
	tables := make([]storage.TenantSnapshotTable, 0, 5)
	for _, spec := range []struct {
		name, query string
	}{
		{"folders", `SELECT id || char(31) || name || char(31) || position FROM folders WHERE workspace_id=? ORDER BY id`},
		{"notes", `SELECT id || char(31) || title || char(31) || content || char(31) || revision FROM notes WHERE workspace_id=? ORDER BY id`},
		{"task_projects", `SELECT id || char(31) || name || char(31) || color FROM task_projects WHERE workspace_id=? ORDER BY id`},
		{"tasks", `SELECT id || char(31) || title || char(31) || status || char(31) || priority FROM tasks WHERE workspace_id=? ORDER BY id`},
		{"tenant_job_outbox", `SELECT id || char(31) || topic || char(31) || aggregate_id || char(31) || aggregate_revision || char(31) || payload_json FROM tenant_job_outbox WHERE workspace_id=? ORDER BY id`},
	} {
		rows, err := tx.QueryContext(ctx, spec.query, workspaceID)
		if err != nil {
			return storage.TenantSnapshot{}, err
		}
		hash := sha256.New()
		var count int64
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				_ = rows.Close()
				return storage.TenantSnapshot{}, err
			}
			_, _ = hash.Write([]byte(value))
			_, _ = hash.Write([]byte("\n"))
			count++
		}
		if err := rows.Close(); err != nil {
			return storage.TenantSnapshot{}, err
		}
		tables = append(tables, storage.TenantSnapshotTable{Name: spec.name, Rows: count, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	if err := tx.Commit(); err != nil {
		return storage.TenantSnapshot{}, err
	}
	return storage.TenantSnapshot{WorkspaceID: strings.TrimSpace(workspaceID), InstallationID: installationID, SchemaVersion: schemaVersion, Tables: tables}, nil
}
