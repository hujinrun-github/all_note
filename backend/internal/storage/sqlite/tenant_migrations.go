package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/hujinrun/flowspace/internal/storage"
)

type tenantMigration struct {
	version string
	sql     []byte
}

func verifySQLiteTenantMigrationChecksum(ctx context.Context, db *sql.DB, expectedVersion string) error {
	if expectedVersion == "" {
		return nil
	}
	dir, err := findSQLiteTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadSQLiteTenantMigrations(dir)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.version != expectedVersion {
			continue
		}
		sum := sha256.Sum256(migration.sql)
		var recorded string
		if err := db.QueryRowContext(ctx, `SELECT checksum FROM tenant_schema_migrations WHERE version=?`, expectedVersion).Scan(&recorded); err != nil {
			return err
		}
		if recorded != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("tenant migration %s checksum mismatch", expectedVersion)
		}
		return nil
	}
	return fmt.Errorf("tenant migration file %s is missing", expectedVersion)
}

func (p Provider) MigrateTenant(ctx context.Context, cfg storage.Config) error {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	dir, err := findSQLiteTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadSQLiteTenantMigrations(dir)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tenant_schema_migrations(version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("ensure tenant_schema_migrations: %w", err)
	}
	for _, migration := range migrations {
		if err := applySQLiteTenantMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

func applySQLiteTenantMigration(ctx context.Context, db *sql.DB, migration tenantMigration) error {
	sum := sha256.Sum256(migration.sql)
	checksum := hex.EncodeToString(sum[:])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM tenant_schema_migrations WHERE version=?`, migration.version).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("tenant migration %s checksum mismatch", migration.version)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		committed = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(migration.sql)); err != nil {
		return fmt.Errorf("apply tenant migration %s: %w", migration.version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenant_schema_migrations(version,checksum) VALUES (?,?)`, migration.version, checksum); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func loadSQLiteTenantMigrations(dir string) ([]tenantMigration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	result := make([]tenantMigration, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		result = append(result, tenantMigration{version: name, sql: contents})
	}
	return result, nil
}

func findSQLiteTenantMigrationsDir() (string, error) {
	candidates := []string{filepath.Join("db", "migrations", "tenant", "sqlite"), filepath.Join("backend", "db", "migrations", "tenant", "sqlite")}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations", "tenant", "sqlite")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("SQLite tenant migrations directory not found")
}
