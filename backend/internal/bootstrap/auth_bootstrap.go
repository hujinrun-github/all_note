package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

const (
	bootstrapAdminUserID   = "user_bootstrap_admin"
	bootstrapWorkspaceID   = "workspace_bootstrap_admin"
	bootstrapWorkspaceRole = "owner"
)

var (
	ErrBootstrapAdminRequired   = errors.New("bootstrap admin configuration required")
	ErrBootstrapAdminIncomplete = errors.New("bootstrap admin configuration incomplete")
)

type Config struct {
	AdminEmail    string
	AdminPassword string
	AdminName     string
}

type State struct {
	HasUsers        bool
	HasBusinessData bool
}

type sqlRunner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

type sqlDialect int

const (
	dialectSQLite sqlDialect = iota
	dialectPostgres
)

type defaultFolder struct {
	ID        string
	Name      string
	SortOrder float64
}

var defaultFolders = []defaultFolder{
	{ID: "__uncategorized", Name: "Uncategorized", SortOrder: 0},
	{ID: "__work", Name: "Work", SortOrder: 1},
	{ID: "__personal", Name: "Personal", SortOrder: 2},
}

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

var businessDataChecks = map[string]string{
	"folders":                `id NOT IN ('__uncategorized', '__work', '__personal')`,
	"task_projects":          `id <> 'personal'`,
	"notes":                  `1=1`,
	"tasks":                  `1=1`,
	"learning_roadmaps":      `1=1`,
	"roadmap_nodes":          `1=1`,
	"roadmap_edges":          `1=1`,
	"roadmap_resources":      `1=1`,
	"events":                 `1=1`,
	"inbox":                  `1=1`,
	"sync_targets":           `1=1`,
	"note_sync_state":        `1=1`,
	"note_project_links":     `1=1`,
	"task_recurrence_rules":  `1=1`,
	"task_occurrences":       `1=1`,
	"note_sync_bindings":     `1=1`,
	"sync_external_claims":   `1=1`,
	"note_sync_suppressions": `1=1`,
	"sync_import_tombstones": `1=1`,
	"search_index":           `1=1`,
}

func EnsureAuthReady(ctx context.Context, store storage.Store, cfg Config) error {
	state, err := InspectState(ctx, store)
	if err != nil {
		return err
	}
	if state.HasUsers {
		return nil
	}
	if cfg.Incomplete() {
		return ErrBootstrapAdminIncomplete
	}
	if !cfg.Configured() {
		if state.HasBusinessData {
			return ErrBootstrapAdminRequired
		}
		return nil
	}
	return store.Transact(ctx, func(tx storage.Store) error {
		return createBootstrapAdminAndWorkspace(ctx, tx, cfg, state.HasBusinessData)
	})
}

func (c Config) Configured() bool {
	if strings.TrimSpace(c.AdminEmail) == "" {
		return false
	}
	if strings.TrimSpace(c.AdminName) == "" {
		return false
	}
	if strings.TrimSpace(c.AdminPassword) == "" {
		return false
	}
	return true
}

func (c Config) Incomplete() bool {
	hasAny := strings.TrimSpace(c.AdminEmail) != "" ||
		strings.TrimSpace(c.AdminPassword) != "" ||
		strings.TrimSpace(c.AdminName) != ""
	return hasAny && !c.Configured()
}

func (c Config) Valid() bool {
	return c.Configured()
}

func InspectState(ctx context.Context, store storage.Store) (State, error) {
	runner, err := sqlRunnerFromStore(store)
	if err != nil {
		return State{}, err
	}
	dialect := dialectForStore(store)

	userCount, err := countRows(ctx, runner, dialect, "users", "1=1")
	if err != nil {
		return State{}, fmt.Errorf("inspect bootstrap users: %w", err)
	}
	if userCount > 0 {
		return State{HasUsers: true}, nil
	}

	hasBusinessData, err := inspectBusinessData(ctx, runner, dialect)
	if err != nil {
		return State{}, err
	}
	return State{HasBusinessData: hasBusinessData}, nil
}

func AssignLegacyBusinessData(ctx context.Context, store storage.Store, workspaceID string) error {
	runner, err := sqlRunnerFromStore(store)
	if err != nil {
		return err
	}
	workspaceID, err = workspaceIDFromScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	dialect := dialectForStore(store)

	for _, table := range workspaceScopedTables {
		exists, err := tableExists(ctx, runner, dialect, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		hasColumn, err := columnExists(ctx, runner, dialect, table, "workspace_id")
		if err != nil {
			return err
		}
		if !hasColumn {
			continue
		}
		query := fmt.Sprintf(
			`UPDATE %s SET workspace_id = %s WHERE workspace_id IS NULL OR workspace_id = ''`,
			quoteIdent(table),
			placeholder(dialect, 1),
		)
		if _, err := runner.ExecContext(ctx, query, workspaceID); err != nil {
			return fmt.Errorf("assign %s workspace: %w", table, err)
		}
	}
	return nil
}

func EnsureDefaultWorkspaceData(ctx context.Context, store storage.Store) error {
	runner, err := sqlRunnerFromStore(store)
	if err != nil {
		return err
	}
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	dialect := dialectForStore(store)

	for _, folder := range defaultFolders {
		if err := ensureDefaultFolder(ctx, runner, dialect, workspaceID, folder); err != nil {
			return err
		}
	}
	return ensureDefaultTaskProject(ctx, runner, dialect, workspaceID)
}

func createBootstrapAdminAndWorkspace(ctx context.Context, store storage.Store, cfg Config, hasLegacyData bool) error {
	passwordHash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		return err
	}

	email := strings.TrimSpace(cfg.AdminEmail)
	name := strings.TrimSpace(cfg.AdminName)
	user := &model.User{
		ID:                 bootstrapAdminUserID,
		Email:              email,
		DisplayName:        name,
		PasswordHash:       passwordHash,
		MustChangePassword: false,
		DefaultWorkspaceID: bootstrapWorkspaceID,
		Role:               "admin",
		Status:             "active",
	}
	workspace := &model.Workspace{
		ID:          bootstrapWorkspaceID,
		Name:        name + " Workspace",
		OwnerUserID: bootstrapAdminUserID,
	}

	if err := store.Auth().CreateUser(ctx, user); err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}
	if err := store.Auth().CreateWorkspace(ctx, workspace); err != nil {
		return fmt.Errorf("create bootstrap workspace: %w", err)
	}
	if err := store.Auth().SetDefaultWorkspace(ctx, user.ID, workspace.ID); err != nil {
		return fmt.Errorf("set bootstrap default workspace: %w", err)
	}
	if err := store.Auth().AddWorkspaceMember(ctx, workspace.ID, user.ID, bootstrapWorkspaceRole); err != nil {
		return fmt.Errorf("add bootstrap workspace member: %w", err)
	}

	scopeCtx := auth.ContextWithWorkspaceScope(ctx, workspace.ID)
	if hasLegacyData {
		if err := AssignLegacyBusinessData(scopeCtx, store, workspace.ID); err != nil {
			return err
		}
	}
	if err := EnsureDefaultWorkspaceData(scopeCtx, store); err != nil {
		return err
	}
	return nil
}

func inspectBusinessData(ctx context.Context, runner sqlRunner, dialect sqlDialect) (bool, error) {
	for _, table := range workspaceScopedTables {
		condition, ok := businessDataChecks[table]
		if !ok {
			continue
		}
		exists, err := tableExists(ctx, runner, dialect, table)
		if err != nil {
			return false, err
		}
		if !exists {
			continue
		}
		count, err := countRows(ctx, runner, dialect, table, condition)
		if err != nil {
			return false, fmt.Errorf("inspect bootstrap business data in %s: %w", table, err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func countRows(ctx context.Context, runner sqlRunner, dialect sqlDialect, table, condition string) (int, error) {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s`, quoteIdent(table), condition)
	var count int
	if err := runner.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func ensureDefaultFolder(ctx context.Context, runner sqlRunner, dialect sqlDialect, workspaceID string, folder defaultFolder) error {
	switch dialect {
	case dialectPostgres:
		_, err := runner.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
			VALUES ($1, $2, $3, now(), $4)
			ON CONFLICT (id) DO UPDATE SET
				workspace_id = COALESCE(NULLIF(folders.workspace_id, ''), EXCLUDED.workspace_id)
		`, folder.ID, folder.Name, folder.SortOrder, workspaceID)
		if err != nil {
			return fmt.Errorf("ensure default folder %s: %w", folder.ID, err)
		}
	default:
		_, err := runner.ExecContext(ctx, `
			INSERT INTO folders (id, name, sort_order, created_at, workspace_id)
			VALUES (?, ?, ?, unixepoch(), ?)
			ON CONFLICT(id) DO UPDATE SET
				workspace_id = CASE
					WHEN folders.workspace_id IS NULL OR folders.workspace_id = '' THEN excluded.workspace_id
					ELSE folders.workspace_id
				END
		`, folder.ID, folder.Name, folder.SortOrder, workspaceID)
		if err != nil {
			return fmt.Errorf("ensure default folder %s: %w", folder.ID, err)
		}
	}
	return nil
}

func ensureDefaultTaskProject(ctx context.Context, runner sqlRunner, dialect sqlDialect, workspaceID string) error {
	switch dialect {
	case dialectPostgres:
		_, err := runner.ExecContext(ctx, `
			INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
			VALUES ($1, $2, $3, $4, now(), now(), $5)
			ON CONFLICT (id) DO UPDATE SET
				workspace_id = COALESCE(NULLIF(task_projects.workspace_id, ''), EXCLUDED.workspace_id)
		`, "personal", "Personal", "personal", "Default personal task project", workspaceID)
		if err != nil {
			return fmt.Errorf("ensure default task project: %w", err)
		}
	default:
		_, err := runner.ExecContext(ctx, `
			INSERT INTO task_projects (id, name, type, description, created_at, updated_at, workspace_id)
			VALUES (?, ?, ?, ?, unixepoch(), unixepoch(), ?)
			ON CONFLICT(id) DO UPDATE SET
				workspace_id = CASE
					WHEN task_projects.workspace_id IS NULL OR task_projects.workspace_id = '' THEN excluded.workspace_id
					ELSE task_projects.workspace_id
				END
		`, "personal", "Personal", "personal", "Default personal task project", workspaceID)
		if err != nil {
			return fmt.Errorf("ensure default task project: %w", err)
		}
	}
	return nil
}

func tableExists(ctx context.Context, runner sqlRunner, dialect sqlDialect, table string) (bool, error) {
	var exists bool
	switch dialect {
	case dialectPostgres:
		if err := runner.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = current_schema()
					AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			return false, fmt.Errorf("inspect postgres table %s: %w", table, err)
		}
	default:
		var count int
		if err := runner.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type IN ('table', 'view') AND name = ?
		`, table).Scan(&count); err != nil {
			return false, fmt.Errorf("inspect sqlite table %s: %w", table, err)
		}
		exists = count > 0
	}
	return exists, nil
}

func columnExists(ctx context.Context, runner sqlRunner, dialect sqlDialect, table, column string) (bool, error) {
	switch dialect {
	case dialectPostgres:
		var exists bool
		if err := runner.QueryRowContext(ctx, `
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
	default:
		rows, err := runner.QueryContext(ctx, `PRAGMA table_info(`+quoteIdent(table)+`)`)
		if err != nil {
			return false, fmt.Errorf("inspect sqlite column %s.%s: %w", table, column, err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name string
			var columnType string
			var notNull int
			var defaultValue any
			var primaryKey int
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return false, err
			}
			if name == column {
				return true, nil
			}
		}
		return false, rows.Err()
	}
}

func sqlRunnerFromStore(store storage.Store) (sqlRunner, error) {
	runner, ok := store.(sqlRunner)
	if !ok {
		return nil, fmt.Errorf("storage store %T does not expose bootstrap SQL runner", store)
	}
	return runner, nil
}

func dialectForStore(store storage.Store) sqlDialect {
	if store.Capabilities().TimeRanges {
		return dialectPostgres
	}
	return dialectSQLite
}

func placeholder(dialect sqlDialect, index int) string {
	if dialect == dialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func workspaceIDFromScope(ctx context.Context, fallback string) (string, error) {
	scopeWorkspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err == nil {
		if strings.TrimSpace(fallback) != "" && fallback != scopeWorkspaceID {
			return "", fmt.Errorf("workspace scope %q does not match target workspace %q", scopeWorkspaceID, fallback)
		}
		return scopeWorkspaceID, nil
	}
	if strings.TrimSpace(fallback) == "" {
		return "", err
	}
	return fallback, nil
}

func quoteIdent(name string) string {
	if strings.ContainsAny(name, "\x00\"") {
		panic("invalid SQL identifier")
	}
	return `"` + name + `"`
}
