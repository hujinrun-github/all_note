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

	dir, err := findSQLiteControlMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadSQLiteControlMigrations(dir)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS control_schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`); err != nil {
		return fmt.Errorf("ensure control_schema_migrations: %w", err)
	}
	for _, migration := range migrations {
		if err := applySQLiteControlMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

func applySQLiteControlMigration(ctx context.Context, db *sql.DB, migration controlMigration) error {
	sum := sha256.Sum256(migration.sql)
	checksum := hex.EncodeToString(sum[:])
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin control migration %s: %w", migration.version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var existingChecksum string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM control_schema_migrations WHERE version = ?`, migration.version).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("control migration %s checksum mismatch", migration.version)
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
		return fmt.Errorf("apply control migration %s: %w", migration.version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO control_schema_migrations(version, checksum) VALUES (?, ?)`, migration.version, checksum); err != nil {
		return fmt.Errorf("record control migration %s: %w", migration.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit control migration %s: %w", migration.version, err)
	}
	committed = true
	return nil
}

func loadSQLiteControlMigrations(dir string) ([]controlMigration, error) {
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
	migrations := make([]controlMigration, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, controlMigration{version: name, sql: contents})
	}
	return migrations, nil
}

func verifySQLiteControlMigrations(ctx context.Context, db *sql.DB) error {
	dir, err := findSQLiteControlMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadSQLiteControlMigrations(dir)
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT version, checksum FROM control_schema_migrations`)
	if err != nil {
		return fmt.Errorf("read control migration history: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]string, len(migrations))
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return fmt.Errorf("scan control migration history: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate control migration history: %w", err)
	}
	if len(applied) != len(migrations) {
		return fmt.Errorf("control migration count is %d, expected %d", len(applied), len(migrations))
	}
	for _, migration := range migrations {
		sum := sha256.Sum256(migration.sql)
		want := hex.EncodeToString(sum[:])
		got, ok := applied[migration.version]
		if !ok {
			return fmt.Errorf("control migration %s is missing", migration.version)
		}
		if got != want {
			return fmt.Errorf("control migration %s checksum mismatch", migration.version)
		}
	}
	return nil
}

func findSQLiteControlMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("db", "migrations", "control", "sqlite"),
		filepath.Join("backend", "db", "migrations", "control", "sqlite"),
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations", "control", "sqlite")}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("SQLite control migrations directory not found")
}
