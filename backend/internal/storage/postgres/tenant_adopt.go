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

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/adopt"
)

const postgresTenantAdoptLockSQL = `hashtext('flowspace_tenant_schema_migrations')`

// AdoptExistingTenant records an existing mixed-schema PostgreSQL database as
// the tenant baseline without replaying the destructive baseline DDL over the
// legacy tables. The operator remains responsible for taking a physical backup
// before invoking this maintenance operation.
func (p Provider) AdoptExistingTenant(ctx context.Context, cfg storage.Config, requested storage.AdoptManifest) error {
	manifestPath, err := findPostgresTenantAdoptManifest()
	if err != nil {
		return err
	}
	manifest, err := adopt.LoadFile(manifestPath)
	if err != nil {
		return err
	}
	if err := manifest.Verify(requested.ID, requested.Checksum, "postgres", "tenant"); err != nil {
		return err
	}

	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open tenant adoption connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(`+postgresTenantAdoptLockSQL+`)`); err != nil {
		return fmt.Errorf("lock tenant adoption: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+postgresTenantAdoptLockSQL+`)`)
		}
	}()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tenant adoption: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, table := range manifest.RequiredTables {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1 AND table_type = 'BASE TABLE'
		`, table).Scan(&count); err != nil {
			return fmt.Errorf("inspect legacy tenant table %s: %w", table, err)
		}
		if count != 1 {
			return fmt.Errorf("legacy tenant table %s is missing", table)
		}
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS tenant_schema_migrations(
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS tenant_installations(
			singleton_key SMALLINT PRIMARY KEY CHECK (singleton_key = 1),
			installation_id UUID NOT NULL UNIQUE,
			schema_identity TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`INSERT INTO tenant_installations(singleton_key, installation_id, schema_identity)
		 VALUES (1, md5(random()::text || clock_timestamp()::text)::uuid, current_schema())
		 ON CONFLICT (singleton_key) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS tenant_capabilities(
			capability TEXT PRIMARY KEY,
			enabled BOOLEAN NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO tenant_capabilities(capability, enabled, detail)
		 VALUES (
		 	'trigram_search',
		 	EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm'),
		 	CASE WHEN EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')
		 		THEN 'pg_trgm installed' ELSE 'portable search' END
		 )
		 ON CONFLICT (capability) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS tenant_workspaces(
			workspace_id TEXT PRIMARY KEY,
			epoch BIGINT NOT NULL DEFAULT 1 CHECK (epoch > 0),
			state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'fenced', 'retired')),
			migration_id TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CHECK ((state = 'fenced' AND migration_id IS NOT NULL) OR
			       (state <> 'fenced' AND migration_id IS NULL))
		)`,
		`INSERT INTO tenant_workspaces(workspace_id)
		 SELECT id FROM workspaces
		 ON CONFLICT (workspace_id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS tenant_job_outbox(
			id TEXT NOT NULL,
			workspace_id TEXT NOT NULL REFERENCES tenant_workspaces(workspace_id) ON DELETE CASCADE,
			topic TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			aggregate_revision BIGINT NOT NULL CHECK (aggregate_revision > 0),
			payload_json JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			published_at TIMESTAMPTZ,
			PRIMARY KEY (workspace_id, id),
			UNIQUE (workspace_id, topic, aggregate_id, aggregate_revision)
		)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("adopt legacy tenant: %w", err)
		}
	}

	dir, err := findPostgresTenantMigrationsDir()
	if err != nil {
		return err
	}
	migrations, err := loadPostgresTenantMigrations(dir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return errors.New("tenant baseline migration is missing")
	}
	baseline := migrations[0]
	sum := sha256.Sum256(baseline.sql)
	checksum := hex.EncodeToString(sum[:])
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tenant_schema_migrations(version, checksum)
		VALUES ($1, $2)
		ON CONFLICT (version) DO NOTHING
	`, baseline.version, checksum); err != nil {
		return fmt.Errorf("record adopted tenant baseline: %w", err)
	}
	var recorded string
	if err := tx.QueryRowContext(ctx, `
		SELECT checksum FROM tenant_schema_migrations WHERE version = $1
	`, baseline.version).Scan(&recorded); err != nil {
		return fmt.Errorf("verify adopted tenant baseline: %w", err)
	}
	if recorded != checksum {
		return fmt.Errorf("tenant migration %s checksum mismatch", baseline.version)
	}

	for _, table := range []string{"folders", "notes", "task_projects", "tasks"} {
		var orphanCount int
		query := fmt.Sprintf(`
			SELECT COUNT(*)
			FROM %s legacy
			LEFT JOIN tenant_workspaces anchor ON anchor.workspace_id = legacy.workspace_id
			WHERE legacy.workspace_id IS NULL OR anchor.workspace_id IS NULL
		`, quotePostgresAdoptIdentifier(table))
		if err := tx.QueryRowContext(ctx, query).Scan(&orphanCount); err != nil {
			return fmt.Errorf("validate legacy tenant table %s: %w", table, err)
		}
		if orphanCount != 0 {
			return fmt.Errorf("legacy tenant table %s has %d rows without a workspace anchor", table, orphanCount)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tenant adoption: %w", err)
	}
	committed = true
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(`+postgresTenantAdoptLockSQL+`)`); err != nil {
		return fmt.Errorf("unlock tenant adoption: %w", err)
	}
	locked = false
	return nil
}

func quotePostgresAdoptIdentifier(value string) string {
	return `"` + value + `"`
}

func findPostgresTenantAdoptManifest() (string, error) {
	candidates := []string{
		filepath.Join("db", "adopt", "tenant", "postgres", "legacy_v1.json"),
		filepath.Join("backend", "db", "adopt", "tenant", "postgres", "legacy_v1.json"),
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append([]string{
			filepath.Join(filepath.Dir(file), "..", "..", "..", "db", "adopt", "tenant", "postgres", "legacy_v1.json"),
		}, candidates...)
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("PostgreSQL tenant adopt manifest not found")
}
