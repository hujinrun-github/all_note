package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestProviderValidateRequiresDatabaseURL(t *testing.T) {
	provider := Provider{}
	err := provider.Validate(storage.Config{Env: "test", Driver: storage.DriverPostgres})
	if err == nil {
		t.Fatal("expected error when database URL is empty")
	}
}

func TestProviderOpenConnectsMigratesHealthAndReportsCapabilities(t *testing.T) {
	schema := fmt.Sprintf("fs_test_provider_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)
	provider := Provider{}

	openedStore, err := provider.Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    databaseURL,
	})
	if err != nil {
		t.Fatalf("open postgres provider: %v", err)
	}
	defer openedStore.Close()

	if err := openedStore.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}

	capabilities := openedStore.Capabilities()
	if !capabilities.FullTextSearch ||
		!capabilities.PrefixSearch ||
		!capabilities.TrigramSearch ||
		!capabilities.JSONObjects ||
		!capabilities.ArrayColumns ||
		!capabilities.TimeRanges ||
		!capabilities.AdvisoryLocks {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}

	dbStore, ok := openedStore.(*store)
	if !ok {
		t.Fatalf("expected *store, got %T", openedStore)
	}
	assertRowCount(t, dbStore.db, `SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init_postgres.sql'`, 1)
}

func TestProviderOpenExposesAuthFinalizerWithoutRunningIt(t *testing.T) {
	schema := fmt.Sprintf("fs_test_provider_finalizer_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)

	openedStore, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    databaseURL,
	})
	if err != nil {
		t.Fatalf("open postgres provider: %v", err)
	}
	defer openedStore.Close()

	finalizer, ok := openedStore.(interface {
		FinalizeAuthSchema(context.Context) error
	})
	if !ok {
		t.Fatalf("expected postgres store to expose FinalizeAuthSchema")
	}

	dbStore := openedStore.(*store)
	assertPostgresColumnNullable(t, dbStore.db, schema, "folders", "workspace_id", "YES")
	if err := finalizer.FinalizeAuthSchema(context.Background()); err == nil {
		t.Fatal("expected direct finalizer before bootstrap/backfill to fail")
	}
	assertPostgresColumnNullable(t, dbStore.db, schema, "folders", "workspace_id", "YES")
}

func TestProviderTransactRejectsNilCallback(t *testing.T) {
	store := openProviderTestStore(t)
	defer store.Close()

	err := store.Transact(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "callback is nil") {
		t.Fatalf("expected nil callback error, got %v", err)
	}
}

func TestProviderTransactCommitRollbackAndPanic(t *testing.T) {
	openedStore := openProviderTestStore(t)
	defer openedStore.Close()

	dbStore := openedStore.(*store)
	if _, err := dbStore.db.Exec(`
		CREATE TABLE provider_transaction_probe (
			id TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create transaction probe: %v", err)
	}

	if err := openedStore.Transact(context.Background(), func(transactional storage.Store) error {
		txStore, ok := transactional.(*storeTx)
		if !ok {
			return fmt.Errorf("expected *storeTx, got %T", transactional)
		}
		_, err := txStore.tx.ExecContext(context.Background(), `
			INSERT INTO provider_transaction_probe (id, value) VALUES ('commit', 'kept')
		`)
		return err
	}); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	assertRowCount(t, dbStore.db, `SELECT COUNT(*) FROM provider_transaction_probe WHERE id = 'commit'`, 1)

	expectedErr := errors.New("force rollback")
	err := openedStore.Transact(context.Background(), func(transactional storage.Store) error {
		txStore := transactional.(*storeTx)
		if _, err := txStore.tx.ExecContext(context.Background(), `
			INSERT INTO provider_transaction_probe (id, value) VALUES ('rollback', 'discarded')
		`); err != nil {
			return err
		}
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected rollback error, got %v", err)
	}
	assertRowCount(t, dbStore.db, `SELECT COUNT(*) FROM provider_transaction_probe WHERE id = 'rollback'`, 0)

	panicValue := "force panic rollback"
	recovered := recoverProviderTransactPanic(t, openedStore, panicValue)
	if recovered != panicValue {
		t.Fatalf("expected panic %q, got %#v", panicValue, recovered)
	}
	assertRowCount(t, dbStore.db, `SELECT COUNT(*) FROM provider_transaction_probe WHERE id = 'panic'`, 0)
}

func openProviderTestStore(t *testing.T) storage.Store {
	t.Helper()

	schema := fmt.Sprintf("fs_test_provider_tx_%d", time.Now().UnixNano())
	databaseURL := createPostgresTestSchema(t, schema)
	store, err := (Provider{}).Open(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    databaseURL,
	})
	if err != nil {
		t.Fatalf("open postgres provider: %v", err)
	}
	return store
}

func recoverProviderTransactPanic(t *testing.T, store storage.Store, panicValue string) (recovered any) {
	t.Helper()

	defer func() {
		recovered = recover()
	}()
	_ = store.Transact(context.Background(), func(transactional storage.Store) error {
		txStore := transactional.(*storeTx)
		if _, err := txStore.tx.ExecContext(context.Background(), `
			INSERT INTO provider_transaction_probe (id, value) VALUES ('panic', 'discarded')
		`); err != nil {
			t.Fatalf("insert panic row: %v", err)
		}
		panic(panicValue)
	})
	t.Fatal("expected transaction callback to panic")
	return nil
}
