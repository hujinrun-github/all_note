package tenantmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type SQLDialect string

const (
	SQLDialectPostgres SQLDialect = "postgres"
	SQLDialectSQLite   SQLDialect = "sqlite"
)

type SQLSnapshot struct {
	tx          *sql.Tx
	dialect     SQLDialect
	workspaceID string
}

func NewSQLSnapshot(ctx context.Context, db *sql.DB, dialect SQLDialect, workspaceID string) (*SQLSnapshot, error) {
	if db == nil || (dialect != SQLDialectPostgres && dialect != SQLDialectSQLite) || strings.TrimSpace(workspaceID) == "" {
		return nil, errors.New("valid snapshot database, dialect, and workspace are required")
	}
	options := &sql.TxOptions{ReadOnly: true}
	if dialect == SQLDialectPostgres {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &SQLSnapshot{tx: tx, dialect: dialect, workspaceID: workspaceID}, nil
}

func (s *SQLSnapshot) Workspace(ctx context.Context) (string, string, error) {
	var state string
	err := s.tx.QueryRowContext(ctx, `SELECT state FROM tenant_workspaces WHERE workspace_id=`+s.placeholder(1), s.workspaceID).Scan(&state)
	return s.workspaceID, state, err
}

func (s *SQLSnapshot) Schema(ctx context.Context) (string, map[string]bool, error) {
	var version string
	if err := s.tx.QueryRowContext(ctx, `SELECT version FROM tenant_schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		return "", nil, err
	}
	rows, err := s.tx.QueryContext(ctx, `SELECT capability,enabled FROM tenant_capabilities ORDER BY capability`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	capabilities := map[string]bool{}
	for rows.Next() {
		var name string
		var enabled bool
		if err := rows.Scan(&name, &enabled); err != nil {
			return "", nil, err
		}
		capabilities[name] = enabled
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	return version, capabilities, nil
}

func (s *SQLSnapshot) ReadTable(ctx context.Context, table LogicalTable) ([]LogicalRow, error) {
	if !knownLogicalTable(table) {
		return nil, errors.New("unknown logical table")
	}
	query := `SELECT ` + quoteColumns(table.Columns) + ` FROM ` + QuoteIdentifier(table.Name) + ` WHERE workspace_id=` + s.placeholder(1) + ` ORDER BY ` + quoteColumns(table.PrimaryKey)
	result, err := s.tx.QueryContext(ctx, query, s.workspaceID)
	if err != nil {
		return nil, err
	}
	defer result.Close()
	rows := []LogicalRow{}
	for result.Next() {
		values := make([]any, len(table.Columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := result.Scan(pointers...); err != nil {
			return nil, err
		}
		row := LogicalRow{}
		for i, column := range table.Columns {
			row[column] = values[i]
		}
		rows = append(rows, row)
	}
	return rows, result.Err()
}

func (s *SQLSnapshot) Close() error {
	if s.tx == nil {
		return nil
	}
	return s.tx.Rollback()
}
func (s *SQLSnapshot) placeholder(index int) string {
	if s.dialect == SQLDialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

type ActiveBindingGuard func(context.Context, string) (bool, error)
type SQLImportTarget struct {
	DB            *sql.DB
	Dialect       SQLDialect
	ActiveBinding ActiveBindingGuard
}

func (t SQLImportTarget) BeginImport(ctx context.Context) (ImportTransaction, error) {
	if t.DB == nil || (t.Dialect != SQLDialectPostgres && t.Dialect != SQLDialectSQLite) {
		return nil, errors.New("valid import database and dialect are required")
	}
	tx, err := t.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	return &sqlImportTx{tx: tx, dialect: t.Dialect, activeBinding: t.ActiveBinding}, nil
}

type sqlImportTx struct {
	tx            *sql.Tx
	dialect       SQLDialect
	activeBinding ActiveBindingGuard
	closed        bool
}

func (t *sqlImportTx) placeholder(index int) string {
	if t.dialect == SQLDialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}
func (t *sqlImportTx) WorkspaceState(ctx context.Context, workspaceID string) (string, string, error) {
	var state string
	var migration sql.NullString
	err := t.tx.QueryRowContext(ctx, `SELECT state,migration_id FROM tenant_workspaces WHERE workspace_id=`+t.placeholder(1), workspaceID).Scan(&state, &migration)
	if errors.Is(err, sql.ErrNoRows) {
		return "missing", "", nil
	}
	return state, migration.String, err
}
func (t *sqlImportTx) HasActiveBinding(ctx context.Context, workspaceID string) (bool, error) {
	if t.activeBinding == nil {
		return false, errors.New("active binding guard is required for retired replacement")
	}
	return t.activeBinding(ctx, workspaceID)
}
func (t *sqlImportTx) DeleteWorkspace(ctx context.Context, workspaceID string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM tenant_workspaces WHERE workspace_id=`+t.placeholder(1), workspaceID)
	return err
}
func (t *sqlImportTx) PrepareFenced(ctx context.Context, workspaceID, migrationID string) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO tenant_workspaces(workspace_id,epoch,state,migration_id) VALUES (`+t.placeholder(1)+`,`+t.placeholder(2)+`,'fenced',`+t.placeholder(3)+`)`, workspaceID, 1, migrationID)
	return err
}
func (t *sqlImportTx) InsertRows(ctx context.Context, table LogicalTable, rows []LogicalRow) error {
	if !knownLogicalTable(table) {
		return errors.New("unknown logical table")
	}
	if len(rows) == 0 {
		return nil
	}
	placeholders := make([]string, len(table.Columns))
	for i := range placeholders {
		placeholders[i] = t.placeholder(i + 1)
	}
	query := `INSERT INTO ` + QuoteIdentifier(table.Name) + ` (` + quoteColumns(table.Columns) + `) VALUES (` + strings.Join(placeholders, ",") + `)`
	for _, row := range rows {
		args := make([]any, len(table.Columns))
		for i, column := range table.Columns {
			args[i] = row[column]
		}
		if _, err := t.tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
func (t *sqlImportTx) Commit() error {
	if t.closed {
		return errors.New("import transaction is closed")
	}
	t.closed = true
	return t.tx.Commit()
}
func (t *sqlImportTx) Rollback() error {
	if t.closed {
		return nil
	}
	t.closed = true
	return t.tx.Rollback()
}

func knownLogicalTable(table LogicalTable) bool {
	for _, known := range BaselineLogicalTables() {
		if table.Name == known.Name && strings.Join(table.Columns, "\x00") == strings.Join(known.Columns, "\x00") {
			return true
		}
	}
	return false
}
func quoteColumns(columns []string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = QuoteIdentifier(column)
	}
	return strings.Join(quoted, ",")
}
