package tenantmigration

import (
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestEndpointURLForDatabaseDoesNotLeakOrCarryDatabase(t *testing.T) {
	endpoint, err := ParsePostgresEndpoint("postgres://user:secret@db.example:5432/app?sslmode=require", "tenant_a")
	if err != nil {
		t.Fatal(err)
	}
	maintenanceURL, err := endpoint.URLForDatabase("flowspace_maintenance")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(maintenanceURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/flowspace_maintenance" || parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("unexpected maintenance URL %q", maintenanceURL)
	}
	if _, err := endpoint.URLForDatabase("bad-name"); err == nil {
		t.Fatal("unsafe maintenance database accepted")
	}
}

func TestEndpointURLWithSchemaIsGeneratedAfterValidation(t *testing.T) {
	endpoint, err := ParsePostgresEndpoint("postgres://user:secret@db.example/app?sslmode=require", "tenant_a")
	if err != nil {
		t.Fatal(err)
	}
	rawURL, err := endpoint.URLWithSchema()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(rawURL)
	if parsed.Query().Get("search_path") != "tenant_a" {
		t.Fatalf("search_path = %q", parsed.Query().Get("search_path"))
	}
}

func TestPostgresStateClassification(t *testing.T) {
	if !IsDatabaseMissing(&pgconn.PgError{Code: "3D000"}) {
		t.Fatal("3D000 was not classified as missing database")
	}
	if IsDatabaseMissing(&pgconn.PgError{Code: "28P01"}) {
		t.Fatal("authentication error was classified as missing database")
	}
	if !IsDuplicateDatabase(&pgconn.PgError{Code: "42P04"}) {
		t.Fatal("42P04 was not classified as duplicate database")
	}
}

func TestInitializationErrorDoesNotExposeURLPassword(t *testing.T) {
	err := initializationError("create database", "postgres://user:secret@db/app")
	if strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "create database") {
		t.Fatalf("unsafe initialization error: %v", err)
	}
}
