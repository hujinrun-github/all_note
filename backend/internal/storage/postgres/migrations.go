package postgres

import (
	"bytes"
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
)

const migrationLockSQL = `hashtext('flowspace_schema_migrations')`

type postgresMigration struct {
	version string
	sql     []byte
}

func RunPostgresMigrations(db *sql.DB) error {
	dir, err := findPostgresMigrationsDir()
	if err != nil {
		return err
	}
	return RunPostgresMigrationsFromDir(db, dir)
}

func RunPostgresMigrationsFromDir(db *sql.DB, dir string) error {
	migrations, err := loadPostgresMigrationsFromDir(dir)
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(`+migrationLockSQL+`)`); err != nil {
		return fmt.Errorf("lock migration runner: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+migrationLockSQL+`)`)
		}
	}()

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	for _, migration := range migrations {
		if err := applyPostgresMigrationOnConn(ctx, conn, migration.version, migration.sql); err != nil {
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(`+migrationLockSQL+`)`); err != nil {
		return fmt.Errorf("unlock migration runner: %w", err)
	}
	locked = false
	return nil
}

func runPostgresMigrations(db *sql.DB) error {
	return RunPostgresMigrations(db)
}

func loadPostgresMigrationsFromDir(dir string) ([]postgresMigration, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("postgres migrations directory is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read postgres migrations directory %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	migrations := make([]postgresMigration, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read postgres migration %s: %w", name, err)
		}
		migrations = append(migrations, postgresMigration{version: name, sql: sqlBytes})
	}
	return migrations, nil
}

func findPostgresMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("db", "migrations", "postgres"),
		filepath.Join("backend", "db", "migrations", "postgres"),
		filepath.Join("..", "..", "..", "db", "migrations", "postgres"),
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{
			filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "migrations", "postgres"),
		}, candidates...)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("postgres migrations directory not found")
}

func applyPostgresMigration(db *sql.DB, version string, sqlBytes []byte) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection %s: %w", version, err)
	}
	defer conn.Close()
	return applyPostgresMigrationOnConn(ctx, conn, version, sqlBytes)
}

func applyPostgresMigrationOnConn(ctx context.Context, conn *sql.Conn, version string, sqlBytes []byte) error {
	sum := sha256.Sum256(sqlBytes)
	checksum := hex.EncodeToString(sum[:])

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+migrationLockSQL+`)`); err != nil {
		return fmt.Errorf("lock migration runner %s: %w", version, err)
	}

	var existingChecksum string
	err = tx.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = $1`, version).Scan(&existingChecksum)
	if err == nil {
		if existingChecksum != checksum {
			return fmt.Errorf("migration %s checksum mismatch: database=%s file=%s", version, existingChecksum, checksum)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit skipped migration %s: %w", version, err)
		}
		committed = true
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check migration %s: %w", version, err)
	}

	if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
		return wrapPostgresMigrationError(version, sqlBytes, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, checksum)
		VALUES ($1, $2)
	`, version, checksum); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}
	committed = true
	return nil
}

func wrapPostgresMigrationError(version string, sqlBytes []byte, err error) error {
	if referencesPgTrgm(sqlBytes) && isLikelyPgTrgmBootstrapError(err) {
		return fmt.Errorf("apply migration %s: pg_trgm extension privilege/bootstrap failed; ensure pg_trgm is installed or run with a role allowed to CREATE EXTENSION pg_trgm WITH SCHEMA public: %w", version, err)
	}
	return fmt.Errorf("apply migration %s: %w", version, err)
}

func referencesPgTrgm(sqlBytes []byte) bool {
	return bytes.Contains(sqlBytes, []byte("pg_trgm")) || bytes.Contains(sqlBytes, []byte("gin_trgm_ops"))
}

func isLikelyPgTrgmBootstrapError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "pg_trgm") ||
		strings.Contains(message, "gin_trgm_ops") ||
		strings.Contains(message, "create extension") ||
		strings.Contains(message, "must be superuser") ||
		strings.Contains(message, "permission denied")
}
