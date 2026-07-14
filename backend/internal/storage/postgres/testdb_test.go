package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/testsupport"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func openPostgresTestDB(t *testing.T, schema string) *sql.DB {
	t.Helper()

	schemaURL := createPostgresTestSchema(t, schema)
	db, err := sql.Open("pgx", schemaURL)
	if err != nil {
		t.Fatalf("open postgres schema connection: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func createPostgresTestSchema(t *testing.T, schema string) string {
	t.Helper()

	if err := validatePostgresTestSchemaName(schema); err != nil {
		t.Fatalf("unsafe postgres test schema name: %v", err)
	}

	baseURL, ready, err := testsupport.IntegrationTarget("PostgreSQL", "FLOWSPACE_TEST_DATABASE_URL", "FLOWSPACE_REQUIRE_POSTGRES_TESTS")
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for postgres integration tests")
	}

	adminDB, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	quotedSchema := quotePostgresIdentifier(schema)
	if _, err := adminDB.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`); err != nil {
		_ = adminDB.Close()
		t.Fatalf("drop test schema: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_ = adminDB.Close()
	})

	return postgresTestURLForSchema(t, baseURL, schema)
}

func TestValidatePostgresTestSchemaNameRejectsUnsafeNames(t *testing.T) {
	for _, schema := range []string{"", "public", "pg_catalog", "pg_temp_1", "fs_prod_data", "fs_test_bad-name"} {
		if err := validatePostgresTestSchemaName(schema); err == nil {
			t.Fatalf("expected schema %q to be rejected", schema)
		}
	}
	if err := validatePostgresTestSchemaName("fs_test_safe_123"); err != nil {
		t.Fatalf("expected safe schema name to be accepted: %v", err)
	}
}

func postgresTestURLForSchema(t *testing.T, baseURL, schema string) string {
	t.Helper()

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse postgres URL: %v", err)
	}
	query := parsed.Query()
	query.Set("options", "-c search_path="+schema+",public")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

var postgresTestSchemaPattern = regexp.MustCompile(`^fs_test_[A-Za-z0-9_]+$`)

func validatePostgresTestSchemaName(schema string) error {
	switch {
	case schema == "":
		return fmt.Errorf("schema name is empty")
	case schema == "public":
		return fmt.Errorf("refusing to manage public schema")
	case strings.HasPrefix(schema, "pg_"):
		return fmt.Errorf("refusing to manage postgres system schema %q", schema)
	case !postgresTestSchemaPattern.MatchString(schema):
		return fmt.Errorf("schema name %q must match fs_test_[A-Za-z0-9_]+", schema)
	default:
		return nil
	}
}

func assertRowCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()

	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query row count: %v", err)
	}
	if got != want {
		t.Fatalf("expected row count %d, got %d for %s", want, got, query)
	}
}
