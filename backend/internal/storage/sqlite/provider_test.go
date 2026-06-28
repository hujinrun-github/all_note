package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestProviderValidateRequiresSQLitePath(t *testing.T) {
	provider := Provider{}
	err := provider.Validate(storage.Config{Env: "test", Driver: storage.DriverSQLite})
	if err == nil {
		t.Fatal("expected missing sqlite path to fail")
	}
}

func TestProviderOpenUsesLegacySchemaAndReportsCapabilities(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.test.db")
	provider := Provider{}

	opened, err := provider.Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite provider: %v", err)
	}
	defer opened.Close()

	if err := opened.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}
	capabilities := opened.Capabilities()
	if !capabilities.FullTextSearch || !capabilities.PrefixSearch {
		t.Fatalf("expected sqlite search capabilities, got %#v", capabilities)
	}
	if capabilities.TrigramSearch || capabilities.ArrayColumns || capabilities.TimeRanges || capabilities.AdvisoryLocks {
		t.Fatalf("unexpected postgres-only capabilities: %#v", capabilities)
	}

	sqliteStore, ok := opened.(*store)
	if !ok {
		t.Fatalf("expected sqlite store, got %T", opened)
	}
	for _, table := range []string{"folders", "notes", "task_projects", "roadmap_nodes"} {
		assertTableExists(t, sqliteStore, table)
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{"sync_targets", "config_json"},
		{"tasks", "project_id"},
		{"tasks", "content"},
	} {
		assertColumnExists(t, sqliteStore, column.table, column.name)
	}
}

func TestProviderOpenEnsuresAuthSchemaAndDeferredDefaultWorkspace(t *testing.T) {
	store := openTestStore(t)

	for _, table := range []string{"users", "workspaces", "workspace_members", "sessions", "audit_events"} {
		assertTableExists(t, store, table)
	}
	for _, column := range []struct {
		table string
		name  string
	}{
		{"users", "default_workspace_id"},
		{"folders", "workspace_id"},
		{"notes", "workspace_id"},
		{"tasks", "workspace_id"},
		{"task_projects", "workspace_id"},
	} {
		assertColumnExists(t, store, column.table, column.name)
	}

	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO users (id, email, display_name, password_hash, must_change_password, default_workspace_id, role, status, created_at, updated_at)
		VALUES ('sqlite_user_later', 'sqlite-later@example.com', 'SQLite Later', 'hash', 1, 'sqlite_workspace_later', 'user', 'active', unixepoch(), unixepoch())
	`); err != nil {
		t.Fatalf("insert user before workspace should be deferred: %v", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO workspaces (id, name, owner_user_id, created_at, updated_at)
		VALUES ('sqlite_workspace_later', 'SQLite Later Workspace', 'sqlite_user_later', unixepoch(), unixepoch())
	`); err != nil {
		t.Fatalf("insert later workspace: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit deferred default workspace FK: %v", err)
	}
}

func TestProviderOpenUpgradesLegacySyncSchemaBeforeInitializingFreshSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "flowspace.legacy-sync.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE sync_targets (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			vault_path TEXT NOT NULL,
			base_folder TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_sync INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE note_sync_state (
			note_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			external_path TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			error_message TEXT,
			PRIMARY KEY (note_id, target_id)
		);
	`); err != nil {
		t.Fatalf("create legacy sync schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy sqlite: %v", err)
	}

	opened, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open upgraded sqlite provider: %v", err)
	}
	defer opened.Close()

	target := &model.SyncTarget{
		ID:         "legacy-default-target",
		Type:       "notion",
		Name:       "Legacy Default Target",
		ConfigJSON: `{}`,
		Enabled:    true,
		IsDefault:  true,
	}
	if err := opened.Sync().SaveTarget(context.Background(), target); err != nil {
		t.Fatalf("save default target after upgrade: %v", err)
	}
	defaultTarget, err := opened.Sync().GetDefaultTarget(context.Background(), "notion")
	if err != nil {
		t.Fatalf("get default target after upgrade: %v", err)
	}
	if defaultTarget.ID != target.ID {
		t.Fatalf("default target ID = %q, want %q", defaultTarget.ID, target.ID)
	}

	note, err := opened.Notes().Create(context.Background(), &model.CreateNoteRequest{
		Title:    "Legacy Binding Note",
		Body:     "Body",
		FolderID: "__uncategorized",
		Tags:     `[]`,
	})
	if err != nil {
		t.Fatalf("create note after upgrade: %v", err)
	}
	if err := opened.Sync().PutBinding(context.Background(), model.NoteSyncBinding{
		NoteID:   note.ID,
		TargetID: target.ID,
	}); err != nil {
		t.Fatalf("put binding after upgrade: %v", err)
	}
	binding, err := opened.Sync().GetBinding(context.Background(), note.ID)
	if err != nil {
		t.Fatalf("get binding after upgrade: %v", err)
	}
	if binding.TargetID != target.ID {
		t.Fatalf("binding target = %q, want %q", binding.TargetID, target.ID)
	}

	if err := opened.Sync().SaveTarget(context.Background(), &model.SyncTarget{
		ID:         "unsupported-target",
		Type:       "unsupported",
		Name:       "Unsupported Target",
		ConfigJSON: `{}`,
		Enabled:    true,
	}); err == nil {
		t.Fatal("expected unsupported sync target type to fail after legacy upgrade")
	}

	if err := opened.Sync().SaveTarget(context.Background(), &model.SyncTarget{
		ID:         "duplicate-name-target",
		Type:       target.Type,
		Name:       target.Name,
		ConfigJSON: `{}`,
		Enabled:    true,
	}); err == nil {
		t.Fatal("expected duplicate sync target type/name to fail after legacy upgrade")
	}
}

func TestStoreTransactRollsBackOnError(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TABLE transact_probe (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	expectedErr := errors.New("rollback transaction")
	err := store.Transact(context.Background(), func(txStore storage.Store) error {
		tx, ok := txStore.(*storeTx)
		if !ok {
			t.Fatalf("expected transaction store, got %T", txStore)
		}
		if _, err := tx.tx.ExecContext(context.Background(), `INSERT INTO transact_probe (id, value) VALUES (?, ?)`, "rolled-back", "value"); err != nil {
			t.Fatalf("insert probe row: %v", err)
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected rollback error, got %v", err)
	}

	assertProbeRowCount(t, store, 0)
}

func TestStoreTransactRejectsNilCallback(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TABLE transact_probe (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	if err := store.Transact(context.Background(), nil); err == nil {
		t.Fatal("expected nil transaction callback to fail")
	}

	assertConnectionReusable(t, store)
}

func TestStoreTransactRollsBackAndRethrowsPanic(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TABLE transact_probe (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	recovered := recoverTransactPanic(t, func() {
		_ = store.Transact(context.Background(), func(txStore storage.Store) error {
			tx, ok := txStore.(*storeTx)
			if !ok {
				t.Fatalf("expected transaction store, got %T", txStore)
			}
			if _, err := tx.tx.ExecContext(context.Background(), `INSERT INTO transact_probe (id, value) VALUES (?, ?)`, "panicked", "value"); err != nil {
				t.Fatalf("insert probe row: %v", err)
			}
			panic("panic transaction")
		})
	})
	if recovered != "panic transaction" {
		t.Fatalf("recovered panic = %#v, want %q", recovered, "panic transaction")
	}

	assertProbeRowCount(t, store, 0)
	assertConnectionReusable(t, store)
}

func TestStoreTransactCommitsOnNilError(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TABLE transact_probe (id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	err := store.Transact(context.Background(), func(txStore storage.Store) error {
		tx, ok := txStore.(*storeTx)
		if !ok {
			t.Fatalf("expected transaction store, got %T", txStore)
		}
		_, err := tx.tx.ExecContext(context.Background(), `INSERT INTO transact_probe (id, value) VALUES (?, ?)`, "committed", "value")
		return err
	})
	if err != nil {
		t.Fatalf("commit transaction: %v", err)
	}

	assertProbeRowCount(t, store, 1)
}

func openTestStore(t *testing.T) *store {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "flowspace.test.db")
	provider := Provider{}
	opened, err := provider.Open(context.Background(), storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("open sqlite provider: %v", err)
	}
	t.Cleanup(func() {
		if err := opened.Close(); err != nil {
			t.Fatalf("close sqlite provider: %v", err)
		}
	})
	sqliteStore, ok := opened.(*store)
	if !ok {
		t.Fatalf("expected sqlite store, got %T", opened)
	}
	return sqliteStore
}

func assertTableExists(t *testing.T, store *store, table string) {
	t.Helper()

	var name string
	err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err != nil {
		t.Fatalf("expected table %s to exist: %v", table, err)
	}
}

func assertColumnExists(t *testing.T, store *store, table, column string) {
	t.Helper()

	rows, err := store.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("inspect columns for %s: %v", table, err)
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
			t.Fatalf("scan column for %s: %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %s: %v", table, err)
	}
	t.Fatalf("expected column %s.%s to exist", table, column)
}

func assertProbeRowCount(t *testing.T, store *store, want int) {
	t.Helper()

	var got int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM transact_probe`).Scan(&got); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}
	if got != want {
		t.Fatalf("probe row count = %d, want %d", got, want)
	}
}

func assertConnectionReusable(t *testing.T, store *store) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := store.db.ExecContext(ctx, `INSERT INTO transact_probe (id, value) VALUES (?, ?)`, "after-transact", "value"); err != nil {
		t.Fatalf("expected sqlite connection to be reusable: %v", err)
	}
}

func recoverTransactPanic(t *testing.T, fn func()) (recovered any) {
	t.Helper()

	defer func() {
		recovered = recover()
	}()
	fn()
	t.Fatal("expected transaction callback to panic")
	return nil
}
