package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// OutboxInstallerDialect selects the schema inventory reader and DDL renderer
// used by the explicit legacy outbox installer.
type OutboxInstallerDialect string

const (
	OutboxInstallerDialectPostgres OutboxInstallerDialect = "postgres"
	OutboxInstallerDialectSQLite   OutboxInstallerDialect = "sqlite"
)

// Dialect is a concise compatibility name for callers that already express
// the provider choice as a dialect.
type Dialect = OutboxInstallerDialect

const (
	DialectPostgres = OutboxInstallerDialectPostgres
	DialectSQLite   = OutboxInstallerDialectSQLite
)

var (
	ErrInvalidLegacyOutboxInstallerInput = errors.New("invalid legacy outbox installer input")
	ErrLegacyOutboxInventory             = errors.New("read legacy outbox schema inventory")
	ErrLegacyOutboxInstallation          = errors.New("install legacy outbox triggers")
)

// InstallLegacyOutboxTriggers explicitly installs the complete canonical
// legacy trigger set. It is intentionally not called by database open,
// migration, runtime resolution, or application startup paths.
//
// Fresh-v2 is a validated immediate no-op. In particular, it never inspects
// optional legacy tables. Legacy mode reads the full schema inventory before
// planning and applies the rendered DDL in one transaction, so neither an
// incomplete source schema nor a later DDL failure can leave a partial set.
func InstallLegacyOutboxTriggers(
	ctx context.Context,
	db *sql.DB,
	dialect OutboxInstallerDialect,
	mode TaskDomainSourceMode,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidLegacyOutboxInstallerInput)
	}
	if db == nil {
		return fmt.Errorf("%w: nil database", ErrInvalidLegacyOutboxInstallerInput)
	}
	if dialect != OutboxInstallerDialectPostgres && dialect != OutboxInstallerDialectSQLite {
		return fmt.Errorf("%w: unsupported dialect %q", ErrInvalidLegacyOutboxInstallerInput, dialect)
	}
	if mode != TaskDomainSourceFreshV2 && mode != TaskDomainSourceLegacyWorkspace {
		return fmt.Errorf("%w: unsupported source mode %q", ErrInvalidLegacyOutboxInstallerInput, mode)
	}
	if mode == TaskDomainSourceFreshV2 {
		return nil
	}

	inventory, err := readLegacyOutboxSchemaInventory(ctx, db, dialect, mode)
	if err != nil {
		return err
	}
	plan, err := BuildLegacyOutboxTriggerPlan(inventory)
	if err != nil {
		return err
	}

	var lockSQL string
	var ddl string
	var baselineSQL string
	switch dialect {
	case OutboxInstallerDialectPostgres:
		lockSQL, err = RenderPostgresLegacyOutboxLockSQL(plan)
		if err == nil {
			ddl, err = RenderPostgresLegacyOutboxSQL(plan)
		}
		if err == nil {
			baselineSQL, err = RenderPostgresLegacyOutboxBaselineSQL(plan)
		}
	case OutboxInstallerDialectSQLite:
		ddl, err = RenderSQLiteLegacyOutboxSQL(plan)
		if err == nil {
			baselineSQL, err = RenderSQLiteLegacyOutboxBaselineSQL(plan)
		}
	}
	if err != nil {
		return fmt.Errorf("%w: render %s install SQL: %w", ErrLegacyOutboxInstallation, dialect, err)
	}
	installSQL := lockSQL + ddl + baselineSQL
	if dialect == OutboxInstallerDialectSQLite {
		return installSQLiteLegacyOutboxSQL(ctx, db, installSQL)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin transaction: %w", ErrLegacyOutboxInstallation, err)
	}
	defer func() { _ = tx.Rollback() }()

	// PostgreSQL explicitly locks every source before replacing triggers.
	// SQLite acquires its database writer lock on the first DDL statement.
	// Keeping lock/DDL/baseline in this one transaction closes the otherwise
	// dangerous window in which a preexisting source row could have neither a
	// baseline logical version nor a trigger-generated one.
	if _, err := tx.ExecContext(ctx, installSQL); err != nil {
		return fmt.Errorf("%w: execute %s trigger and baseline transaction: %w", ErrLegacyOutboxInstallation, dialect, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: commit transaction: %w", ErrLegacyOutboxInstallation, err)
	}
	return nil
}

func installSQLiteLegacyOutboxSQL(ctx context.Context, db *sql.DB, installSQL string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: acquire SQLite installer connection: %w", ErrLegacyOutboxInstallation, err)
	}
	defer conn.Close()

	// database/sql's generic BeginTx emits a deferred BEGIN for SQLite. A
	// concurrent legacy writer can then commit after inventory but before the
	// installer upgrades to a writer, and the upgrade may fail immediately
	// with SQLITE_BUSY. BEGIN IMMEDIATE acquires the single-writer reservation
	// first, making trigger replacement and baseline seeding one serialized
	// unit while readers continue normally.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("%w: begin SQLite immediate transaction: %w", ErrLegacyOutboxInstallation, err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()

	if _, err := conn.ExecContext(ctx, installSQL); err != nil {
		return fmt.Errorf("%w: execute sqlite trigger and baseline transaction: %w", ErrLegacyOutboxInstallation, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("%w: commit SQLite trigger and baseline transaction: %w", ErrLegacyOutboxInstallation, err)
	}
	return nil
}

func readLegacyOutboxSchemaInventory(
	ctx context.Context,
	db *sql.DB,
	dialect OutboxInstallerDialect,
	mode TaskDomainSourceMode,
) (SchemaInventory, error) {
	switch dialect {
	case OutboxInstallerDialectPostgres:
		return readPostgresLegacyOutboxSchemaInventory(ctx, db, mode)
	case OutboxInstallerDialectSQLite:
		return readSQLiteLegacyOutboxSchemaInventory(ctx, db, mode)
	default:
		return SchemaInventory{}, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidLegacyOutboxInstallerInput, dialect)
	}
}

func readPostgresLegacyOutboxSchemaInventory(
	ctx context.Context,
	db *sql.DB,
	mode TaskDomainSourceMode,
) (SchemaInventory, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = current_schema()
		 ORDER BY table_name, ordinal_position`)
	if err != nil {
		return SchemaInventory{}, fmt.Errorf("%w: PostgreSQL information_schema.columns: %w", ErrLegacyOutboxInventory, err)
	}
	defer rows.Close()

	wanted := legacyOutboxManifestTableSet()
	tables := make(map[string][]string, len(wanted))
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return SchemaInventory{}, fmt.Errorf("%w: scan PostgreSQL column: %w", ErrLegacyOutboxInventory, err)
		}
		if _, ok := wanted[table]; !ok {
			continue
		}
		tables[table] = append(tables[table], normalizePostgresLegacyOutboxColumn(table, column))
	}
	if err := rows.Err(); err != nil {
		return SchemaInventory{}, fmt.Errorf("%w: iterate PostgreSQL columns: %w", ErrLegacyOutboxInventory, err)
	}
	normalizeInventoryColumns(tables)
	return SchemaInventory{Mode: mode, Tables: tables}, nil
}

func readSQLiteLegacyOutboxSchemaInventory(
	ctx context.Context,
	db *sql.DB,
	mode TaskDomainSourceMode,
) (SchemaInventory, error) {
	manifest := LegacyOutboxManifest()
	tables := make(map[string][]string, len(manifest))
	for _, source := range manifest {
		// The table name is bound rather than interpolated. SQLite inventory is
		// deliberately canonical: unlike PostgreSQL's known historical Event
		// spelling, no SQLite column alias is guessed here.
		rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, source.Table)
		if err != nil {
			return SchemaInventory{}, fmt.Errorf("%w: SQLite pragma_table_info(%s): %w", ErrLegacyOutboxInventory, source.Table, err)
		}
		columns := make([]string, 0, len(source.RequiredColumns))
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				_ = rows.Close()
				return SchemaInventory{}, fmt.Errorf("%w: scan SQLite column for %s: %w", ErrLegacyOutboxInventory, source.Table, err)
			}
			columns = append(columns, column)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return SchemaInventory{}, fmt.Errorf("%w: iterate SQLite columns for %s: %w", ErrLegacyOutboxInventory, source.Table, err)
		}
		if err := rows.Close(); err != nil {
			return SchemaInventory{}, fmt.Errorf("%w: close SQLite inventory for %s: %w", ErrLegacyOutboxInventory, source.Table, err)
		}
		if len(columns) != 0 {
			tables[source.Table] = columns
		}
	}
	normalizeInventoryColumns(tables)
	return SchemaInventory{Mode: mode, Tables: tables}, nil
}

func normalizeSQLiteLegacyOutboxColumn(table, column string) string {
	if table == "tasks" && column == "due" {
		return "due_at"
	}
	return column
}

func legacyOutboxManifestTableSet() map[string]struct{} {
	manifest := LegacyOutboxManifest()
	result := make(map[string]struct{}, len(manifest))
	for _, source := range manifest {
		result[source.Table] = struct{}{}
	}
	return result
}

func normalizePostgresLegacyOutboxColumn(table, column string) string {
	if table == "events" {
		switch column {
		case "start_at":
			return "start_time"
		case "end_at":
			return "end_time"
		}
	}
	return column
}

func normalizeInventoryColumns(tables map[string][]string) {
	for table, columns := range tables {
		sort.Strings(columns)
		if len(columns) < 2 {
			continue
		}
		deduplicated := columns[:1]
		for _, column := range columns[1:] {
			if column != deduplicated[len(deduplicated)-1] {
				deduplicated = append(deduplicated, column)
			}
		}
		tables[table] = deduplicated
	}
}
