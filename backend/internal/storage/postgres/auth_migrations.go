package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var workspaceScopedTables = []string{
	"folders",
	"notes",
	"task_projects",
	"tasks",
	"learning_roadmaps",
	"roadmap_nodes",
	"roadmap_edges",
	"roadmap_resources",
	"events",
	"inbox",
	"sync_targets",
	"note_sync_state",
	"note_project_links",
	"task_recurrence_rules",
	"task_occurrences",
	"note_sync_bindings",
	"sync_external_claims",
	"note_sync_suppressions",
	"sync_import_tombstones",
	"search_index",
}

func runMultiUserAuthFinalizer(ctx context.Context, db *sql.DB) error {
	if err := validateWorkspaceBackfill(ctx, db); err != nil {
		return err
	}
	if err := applyWorkspaceNotNullConstraints(ctx, db); err != nil {
		return err
	}
	if err := applyWorkspaceCompositeKeys(ctx, db); err != nil {
		return err
	}
	return applyWorkspaceCompositeForeignKeys(ctx, db)
}

func validateWorkspaceBackfill(ctx context.Context, db *sql.DB) error {
	if err := validateUsersDefaultWorkspace(ctx, db); err != nil {
		return err
	}
	for _, table := range workspaceScopedTables {
		if err := validateWorkspaceIDBackfill(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}

func validateUsersDefaultWorkspace(ctx context.Context, db *sql.DB) error {
	exists, err := postgresTableExists(ctx, db, "users")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE default_workspace_id IS NULL`).Scan(&count); err != nil {
		return fmt.Errorf("validate users default workspace backfill: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("multi-user auth finalizer: users.default_workspace_id is missing for %d user(s); run workspace backfill before final constraints", count)
	}
	return nil
}

func validateWorkspaceIDBackfill(ctx context.Context, db *sql.DB, table string) error {
	exists, err := postgresTableExists(ctx, db, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasColumn, err := postgresColumnExists(ctx, db, table, "workspace_id")
	if err != nil {
		return err
	}
	if !hasColumn {
		return fmt.Errorf("multi-user auth finalizer: %s.workspace_id column is missing", table)
	}

	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE workspace_id IS NULL`, postgresIdent(table))
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return fmt.Errorf("validate %s.workspace_id backfill: %w", table, err)
	}
	if count > 0 {
		return fmt.Errorf("multi-user auth finalizer: %s.workspace_id is missing for %d row(s); run workspace backfill before final constraints", table, count)
	}
	return nil
}

func applyWorkspaceNotNullConstraints(ctx context.Context, db *sql.DB) error {
	if err := setPostgresColumnNotNull(ctx, db, "users", "default_workspace_id"); err != nil {
		return err
	}
	for _, table := range workspaceScopedTables {
		if err := setPostgresColumnNotNull(ctx, db, table, "workspace_id"); err != nil {
			return err
		}
	}
	return nil
}

func applyWorkspaceCompositeKeys(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS workspaces_owner_id_idx ON workspaces (owner_user_id, id)`); err != nil {
		return fmt.Errorf("ensure workspaces(owner_user_id,id) key: %w", err)
	}
	for _, table := range workspaceScopedTables {
		if err := addWorkspaceCompositeKey(ctx, db, table); err != nil {
			return err
		}
	}
	return nil
}

func applyWorkspaceCompositeForeignKeys(ctx context.Context, db *sql.DB) error {
	exists, err := postgresConstraintExists(ctx, db, "users", "users_default_owned_workspace_fk")
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE users
		  ADD CONSTRAINT users_default_owned_workspace_fk
		  FOREIGN KEY (id, default_workspace_id)
		  REFERENCES workspaces(owner_user_id, id)
		  DEFERRABLE INITIALLY DEFERRED
	`); err != nil {
		return fmt.Errorf("add users default owned workspace foreign key: %w", err)
	}
	return nil
}

func setPostgresColumnNotNull(ctx context.Context, db *sql.DB, table, column string) error {
	stmt := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET NOT NULL", postgresIdent(table), postgresIdent(column))
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("set %s.%s not null: %w", table, column, err)
	}
	return nil
}

func addWorkspaceCompositeKey(ctx context.Context, db *sql.DB, table string) error {
	hasID, err := postgresColumnExists(ctx, db, table, "id")
	if err != nil {
		return err
	}
	if !hasID {
		return nil
	}
	indexName := table + "_workspace_id_id_idx"
	stmt := fmt.Sprintf(
		"CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (workspace_id, id)",
		postgresIdent(indexName),
		postgresIdent(table),
	)
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("ensure %s(workspace_id,id) key: %w", table, err)
	}
	return nil
}

func postgresTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
				AND table_name = $1
		)
	`, table).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect postgres table %s: %w", table, err)
	}
	return exists, nil
}

func postgresColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
				AND table_name = $1
				AND column_name = $2
		)
	`, table, column).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect postgres column %s.%s: %w", table, column, err)
	}
	return exists, nil
}

func postgresConstraintExists(ctx context.Context, db *sql.DB, table, constraint string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint c
			JOIN pg_class rel ON rel.oid = c.conrelid
			JOIN pg_namespace n ON n.oid = rel.relnamespace
			WHERE n.nspname = current_schema()
				AND rel.relname = $1
				AND c.conname = $2
		)
	`, table, constraint).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect postgres constraint %s.%s: %w", table, constraint, err)
	}
	return exists, nil
}

func postgresIdent(name string) string {
	if strings.ContainsAny(name, "\x00\"") {
		panic(errors.New("invalid postgres identifier"))
	}
	return `"` + name + `"`
}
