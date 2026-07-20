package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/adopt"
)

func (p Provider) AdoptExistingTenant(ctx context.Context, cfg storage.Config, requested storage.AdoptManifest) error {
	manifestPath, err := findSQLiteTenantAdoptManifest()
	if err != nil {
		return err
	}
	manifest, err := adopt.LoadFile(manifestPath)
	if err != nil {
		return err
	}
	if err := manifest.Verify(requested.ID, requested.Checksum, "sqlite", "tenant"); err != nil {
		return err
	}
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, table := range manifest.RequiredTables {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("legacy tenant table %s is missing", table)
		}
	}
	if err := db.Close(); err != nil {
		return err
	}
	backupPath := fmt.Sprintf("%s.pre-adopt-%d.bak", cfg.SQLitePath, time.Now().UTC().UnixNano())
	if err := copySQLiteFile(cfg.SQLitePath, backupPath); err != nil {
		return fmt.Errorf("create pre-adopt backup: %w", err)
	}

	db, err = p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS tenant_schema_migrations(version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS tenant_installations(singleton_key INTEGER PRIMARY KEY CHECK(singleton_key=1), installation_id TEXT NOT NULL UNIQUE, schema_identity TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT OR IGNORE INTO tenant_installations(singleton_key,installation_id,schema_identity) VALUES(1,lower(hex(randomblob(16))),'main')`,
		`CREATE TABLE IF NOT EXISTS tenant_capabilities(capability TEXT PRIMARY KEY, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), detail TEXT NOT NULL DEFAULT '')`,
		`INSERT OR IGNORE INTO tenant_capabilities(capability,enabled,detail) VALUES('trigram_search',0,'SQLite tenant uses portable search')`,
		`CREATE TABLE IF NOT EXISTS tenant_workspaces(workspace_id TEXT PRIMARY KEY, epoch INTEGER NOT NULL DEFAULT 1 CHECK(epoch>0), state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','fenced','retired')), migration_id TEXT, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, CHECK((state='fenced' AND migration_id IS NOT NULL) OR (state<>'fenced' AND migration_id IS NULL)))`,
		`INSERT OR IGNORE INTO tenant_workspaces(workspace_id) SELECT id FROM workspaces`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("adopt legacy tenant: %w", err)
		}
	}
	dir, err := findSQLiteTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadSQLiteTenantMigrations(dir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return fmt.Errorf("tenant baseline migration is missing")
	}
	sum := sha256.Sum256(migrations[0].sql)
	if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO tenant_schema_migrations(version,checksum) VALUES(?,?)`, migrations[0].version, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	var violations int
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	for rows.Next() {
		violations++
	}
	_ = rows.Close()
	if violations != 0 {
		return fmt.Errorf("legacy tenant has %d foreign key violations", violations)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

func copySQLiteFile(source, target string) error {
	in, err := os.Open(filepath.Clean(source))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Clean(target), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}

func findSQLiteTenantAdoptManifest() (string, error) {
	candidates := []string{filepath.Join("db", "adopt", "tenant", "sqlite", "legacy_v1.json"), filepath.Join("backend", "db", "adopt", "tenant", "sqlite", "legacy_v1.json")}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "adopt", "tenant", "sqlite", "legacy_v1.json")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("SQLite tenant adopt manifest not found")
}
