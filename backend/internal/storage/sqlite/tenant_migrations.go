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
	if err := prepareSQLiteTenantMigration(ctx, tx, migration.version); err != nil {
		return fmt.Errorf("prepare tenant migration %s: %w", migration.version, err)
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

func prepareSQLiteTenantMigration(ctx context.Context, tx *sql.Tx, version string) error {
	switch version {
	case "0007_mobile_v2_content_domain.sql":
		return prepareSQLiteContentDomainMigration(ctx, tx)
	case "0012_mobile_v2_content_retention.sql":
		return ensureSQLiteTenantNoteColumns(ctx, tx, []struct {
			name       string
			definition string
		}{
			{name: "content", definition: "TEXT NOT NULL DEFAULT ''"},
			{name: "content_text", definition: "TEXT NOT NULL DEFAULT ''"},
		})
	default:
		return nil
	}
}

func prepareSQLiteContentDomainMigration(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name       string
		definition string
	}{
		{name: "client_id", definition: "TEXT"},
		{name: "body", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "tags", definition: "TEXT NOT NULL DEFAULT '[]'"},
		{name: "revision", definition: "INTEGER NOT NULL DEFAULT 1"},
		{name: "deleted_at", definition: "INTEGER"},
	}
	if err := ensureSQLiteTenantNoteColumns(ctx, tx, columns); err != nil {
		return err
	}
	contentTextExists, err := sqliteTenantColumnExists(ctx, tx, "notes", "content_text")
	if err != nil {
		return err
	}
	if contentTextExists {
		if _, err := tx.ExecContext(ctx, `UPDATE notes SET body=content_text WHERE body='' AND content_text<>''`); err != nil {
			return fmt.Errorf("backfill notes.body: %w", err)
		}
	}
	return nil
}

func ensureSQLiteTenantNoteColumns(ctx context.Context, tx *sql.Tx, columns []struct {
	name       string
	definition string
}) error {
	for _, column := range columns {
		exists, err := sqliteTenantColumnExists(ctx, tx, "notes", column.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, "ALTER TABLE notes ADD COLUMN "+column.name+" "+column.definition); err != nil {
			return fmt.Errorf("add notes.%s: %w", column.name, err)
		}
	}
	return nil
}

func sqliteTenantColumnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
