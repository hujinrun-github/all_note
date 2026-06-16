package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

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
