package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
)

func TestProviderInjectedDialContextProtectsEveryPhysicalConnection(t *testing.T) {
	var calls atomic.Int32
	dialContext := func(ctx context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		client, server := net.Pipe()
		go servePostgresPingConnection(server)
		return client, nil
	}
	provider, err := NewProviderWithDialContext(dialContext)
	if err != nil {
		t.Fatalf("new protected provider: %v", err)
	}

	db, err := provider.openWithoutMigrations(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    "postgres://user:secret@database.example:5432/tenant?sslmode=disable",
	})
	if err != nil {
		t.Fatalf("open protected postgres connection: %v", err)
	}
	defer db.Close()

	// Force database/sql to discard the initial idle connection. The next ping
	// must create a new physical pgx connection and must not fall back to DNS.
	db.SetMaxIdleConns(0)
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping replacement physical connection: %v", err)
	}
	if got := calls.Load(); got < 2 {
		t.Fatalf("expected every physical connection to use injected dialer, got %d calls", got)
	}
}

func TestProviderInjectedDialContextPreservesOriginalTLSHostname(t *testing.T) {
	var observedAddress string
	provider, err := NewProviderWithDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		observedAddress = address
		return nil, errors.New("stop before network")
	})
	if err != nil {
		t.Fatalf("new protected provider: %v", err)
	}

	_, err = provider.openWithoutMigrations(context.Background(), storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    "postgres://user:secret@tenant-db.example:5432/tenant?sslmode=require",
	})
	if err == nil {
		t.Fatal("expected injected dial failure")
	}
	if observedAddress != "tenant-db.example:5432" {
		t.Fatalf("expected pgx to retain the original hostname for TLS, got %q", observedAddress)
	}
	config, configErr := provider.connectionConfig("postgres://user:secret@tenant-db.example:5432/tenant?sslmode=verify-full")
	if configErr != nil {
		t.Fatalf("parse protected TLS connection config: %v", configErr)
	}
	if config.TLSConfig == nil || config.TLSConfig.ServerName != "tenant-db.example" {
		t.Fatalf("expected original TLS ServerName, got %#v", config.TLSConfig)
	}
}

func TestNewProviderWithDialContextRejectsNil(t *testing.T) {
	if _, err := NewProviderWithDialContext(nil); err == nil {
		t.Fatal("expected nil protected dialer to be rejected")
	}
}

func TestProtectedProviderTenantWriterRetainsDialContext(t *testing.T) {
	protected, err := NewProviderWithDialContext(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("test dial")
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := protected.NewTenantWriter(storage.Config{Driver: storage.DriverPostgres, URL: "postgres://user@db.example/app"})
	if writer == nil || writer.provider.dialContext == nil {
		t.Fatal("tenant writer discarded the protected provider")
	}
}

func servePostgresPingConnection(conn net.Conn) {
	defer conn.Close()
	if err := readPostgresStartup(conn); err != nil {
		return
	}
	if err := writePostgresBackendMessage(conn, 'R', []byte{0, 0, 0, 0}); err != nil {
		return
	}
	if err := writePostgresBackendMessage(conn, 'K', make([]byte, 8)); err != nil {
		return
	}
	if err := writePostgresBackendMessage(conn, 'Z', []byte{'I'}); err != nil {
		return
	}
	for {
		var messageType [1]byte
		if _, err := io.ReadFull(conn, messageType[:]); err != nil {
			return
		}
		var size uint32
		if err := binary.Read(conn, binary.BigEndian, &size); err != nil || size < 4 {
			return
		}
		payload := make([]byte, size-4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		switch messageType[0] {
		case 'Q':
			if err := writePostgresBackendMessage(conn, 'C', []byte("SELECT 1\x00")); err != nil {
				return
			}
			if err := writePostgresBackendMessage(conn, 'Z', []byte{'I'}); err != nil {
				return
			}
		case 'X':
			return
		default:
			return
		}
	}
}

func readPostgresStartup(conn net.Conn) error {
	var size uint32
	if err := binary.Read(conn, binary.BigEndian, &size); err != nil || size < 8 {
		return errors.New("invalid PostgreSQL startup packet")
	}
	_, err := io.CopyN(io.Discard, conn, int64(size-4))
	return err
}

func writePostgresBackendMessage(conn net.Conn, messageType byte, payload []byte) error {
	if _, err := conn.Write([]byte{messageType}); err != nil {
		return err
	}
	if err := binary.Write(conn, binary.BigEndian, uint32(len(payload)+4)); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

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
