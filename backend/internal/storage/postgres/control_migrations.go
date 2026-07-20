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

const controlMigrationLockSQL = `hashtext('flowspace_control_schema_migrations')`

type controlMigration struct {
	version string
	sql     []byte
}

func (p Provider) MigrateControl(ctx context.Context, cfg storage.Config) error {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	dir, err := findPostgresControlMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadPostgresControlMigrations(dir)
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open control migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(`+controlMigrationLockSQL+`)`); err != nil {
		return fmt.Errorf("lock control migration runner: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+controlMigrationLockSQL+`)`)
		}
	}()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS control_schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("ensure control_schema_migrations: %w", err)
	}
	for _, migration := range migrations {
		if err := applyPostgresControlMigration(ctx, conn, migration); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+controlMigrationLockSQL+`)`); err != nil {
		return fmt.Errorf("unlock control migration runner: %w", err)
	}
	locked = false
	return nil
}

func applyPostgresControlMigration(ctx context.Context, conn *sql.Conn, migration controlMigration) error {
	sum := sha256.Sum256(migration.sql)
	checksum := hex.EncodeToString(sum[:])
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin control migration %s: %w", migration.version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var existing string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM control_schema_migrations WHERE version = $1`, migration.version).Scan(&existing)
	if err == nil {
		if existing != checksum {
			return fmt.Errorf("control migration %s checksum mismatch", migration.version)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit skipped control migration %s: %w", migration.version, err)
		}
		committed = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check control migration %s: %w", migration.version, err)
	}
	if _, err := tx.ExecContext(ctx, string(migration.sql)); err != nil {
		return fmt.Errorf("apply control migration %s: %w", migration.version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_schema_migrations(version, checksum) VALUES ($1, $2)`, migration.version, checksum); err != nil {
		return fmt.Errorf("record control migration %s: %w", migration.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit control migration %s: %w", migration.version, err)
	}
	committed = true
	return nil
}

func loadPostgresControlMigrations(dir string) ([]controlMigration, error) {
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
	result := make([]controlMigration, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		result = append(result, controlMigration{version: name, sql: contents})
	}
	return result, nil
}

func findPostgresControlMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("db", "migrations", "control", "postgres"),
		filepath.Join("backend", "db", "migrations", "control", "postgres"),
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations", "control", "postgres")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("PostgreSQL control migrations directory not found")
}
