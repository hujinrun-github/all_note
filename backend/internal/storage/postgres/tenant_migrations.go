package postgres

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

const tenantMigrationLockSQL = `hashtext('flowspace_tenant_schema_migrations')`

type tenantMigration struct {
	version string
	sql     []byte
}

func verifyPostgresTenantMigrationChecksum(ctx context.Context, db *sql.DB, expectedVersion string) error {
	if expectedVersion == "" {
		return nil
	}
	dir, err := findPostgresTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadPostgresTenantMigrations(dir)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if migration.version != expectedVersion {
			continue
		}
		sum := sha256.Sum256(migration.sql)
		var recorded string
		if err := db.QueryRowContext(ctx, `SELECT checksum FROM tenant_schema_migrations WHERE version=$1`, expectedVersion).Scan(&recorded); err != nil {
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
	dir, err := findPostgresTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadPostgresTenantMigrations(dir)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open tenant migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(`+tenantMigrationLockSQL+`)`); err != nil {
		return fmt.Errorf("lock tenant migration runner: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+tenantMigrationLockSQL+`)`)
		}
	}()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tenant_schema_migrations(version TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("ensure tenant_schema_migrations: %w", err)
	}
	for _, migration := range migrations {
		if err := applyPostgresTenantMigration(ctx, conn, migration); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+tenantMigrationLockSQL+`)`); err != nil {
		return fmt.Errorf("unlock tenant migration runner: %w", err)
	}
	locked = false
	return nil
}

func applyPostgresTenantMigration(ctx context.Context, conn *sql.Conn, migration tenantMigration) error {
	sum := sha256.Sum256(migration.sql)
	checksum := hex.EncodeToString(sum[:])
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tenant migration %s: %w", migration.version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM tenant_schema_migrations WHERE version=$1`, migration.version).Scan(&existing)
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenant_schema_migrations(version,checksum) VALUES ($1,$2)`, migration.version, checksum); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func loadPostgresTenantMigrations(dir string) ([]tenantMigration, error) {
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

func findPostgresTenantMigrationsDir() (string, error) {
	candidates := []string{filepath.Join("db", "migrations", "tenant", "postgres"), filepath.Join("backend", "db", "migrations", "tenant", "postgres")}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations", "tenant", "postgres")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("PostgreSQL tenant migrations directory not found")
}
