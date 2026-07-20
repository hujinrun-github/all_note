package tenantmigration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestParsePostgresEndpointRejectsUnsafeConnectionOptions(t *testing.T) {
	tests := []string{
		"postgres://user:secret@db-a,db-b/app",
		"postgres://user:secret@db/app?host=elsewhere",
		"postgres://user:secret@db/app?options=-csearch_path%3Devil",
		"postgres://user:secret@db/app?service=production",
		"postgres://user:secret@db/app#fragment",
	}
	for _, rawURL := range tests {
		if _, err := ParsePostgresEndpoint(rawURL, "public"); err == nil {
			t.Fatalf("ParsePostgresEndpoint(%q) succeeded, want rejection", rawURL)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error leaked credential: %v", err)
		}
	}
}

func TestParsePostgresEndpointRequiresSafeDatabaseAndSchema(t *testing.T) {
	if _, err := ParsePostgresEndpoint("postgres://user:secret@db/valid_db", `tenant";DROP TABLE notes`); err == nil {
		t.Fatal("unsafe schema was accepted")
	}
	if _, err := ParsePostgresEndpoint("postgres://user:secret@db/bad%2Fname", "public"); err == nil {
		t.Fatal("unsafe database was accepted")
	}
	endpoint, err := ParsePostgresEndpoint("postgres://user:secret@db.example:5432/notes?sslmode=require&connect_timeout=5", "tenant_01")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if endpoint.Database != "notes" || endpoint.Schema != "tenant_01" || endpoint.Host != "db.example" {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
	if got := QuoteIdentifier(`a"b`); got != `"a""b"` {
		t.Fatalf("QuoteIdentifier() = %q", got)
	}
}

type stubIdentityReader struct {
	snapshot NamespaceSnapshot
	err      error
	queries  int
}

func (s *stubIdentityReader) ReadNamespace(context.Context, PostgresEndpoint) (NamespaceSnapshot, error) {
	s.queries++
	return s.snapshot, s.err
}

type stubInitializer struct{ calls int }

func (s *stubInitializer) Initialize(context.Context, PostgresEndpoint) error {
	s.calls++
	return nil
}

func TestPreflightDoesNotInitializeMissingSchema(t *testing.T) {
	reader := &stubIdentityReader{err: sql.ErrNoRows}
	initializer := &stubInitializer{}
	service := PreflightService{Reader: reader, Initializer: initializer}
	_, err := service.Probe(context.Background(), PostgresEndpoint{Database: "notes", Schema: "public"})
	if !errors.Is(err, ErrTenantSchemaNotInitialized) {
		t.Fatalf("Probe() error = %v", err)
	}
	if initializer.calls != 0 {
		t.Fatalf("probe performed %d initialization calls", initializer.calls)
	}
}

func TestPreflightMapsMissingTableWithoutInitializing(t *testing.T) {
	reader := &stubIdentityReader{err: &pgconn.PgError{Code: "42P01"}}
	initializer := &stubInitializer{}
	service := PreflightService{Reader: reader, Initializer: initializer}
	_, err := service.Probe(context.Background(), PostgresEndpoint{Database: "notes", Schema: "public"})
	if !errors.Is(err, ErrTenantSchemaNotInitialized) || initializer.calls != 0 {
		t.Fatalf("Probe() error=%v initialize calls=%d", err, initializer.calls)
	}
}

func TestExplicitInitializeReadsIdentityAfterDDL(t *testing.T) {
	reader := &stubIdentityReader{snapshot: NamespaceSnapshot{Provider: "postgres", InstallationID: "install-1", DatabaseIdentity: "db-42", SchemaIdentity: "public"}}
	initializer := &stubInitializer{}
	service := PreflightService{Reader: reader, Initializer: initializer}
	snapshot, err := service.Initialize(context.Background(), PostgresEndpoint{Database: "notes", Schema: "public"})
	if err != nil {
		t.Fatalf("Initialize(): %v", err)
	}
	if initializer.calls != 1 || reader.queries != 1 || snapshot.InstallationID != "install-1" {
		t.Fatalf("unexpected initialize result calls=%d reads=%d snapshot=%+v", initializer.calls, reader.queries, snapshot)
	}
}
