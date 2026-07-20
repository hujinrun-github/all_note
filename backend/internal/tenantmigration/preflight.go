package tenantmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrTenantSchemaNotInitialized = errors.New("tenant schema is not initialized")
	identifierPattern             = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
)

type PostgresEndpoint struct {
	URL      string
	Host     string
	Port     string
	Database string
	Schema   string
}

var allowedConnectionOptions = map[string]struct{}{
	"application_name": {},
	"connect_timeout":  {},
	"sslmode":          {},
}

func ParsePostgresEndpoint(rawURL, schema string) (PostgresEndpoint, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Hostname() == "" {
		return PostgresEndpoint{}, errors.New("invalid PostgreSQL endpoint")
	}
	if parsed.Fragment != "" || strings.Contains(parsed.Host, ",") {
		return PostgresEndpoint{}, errors.New("unsupported PostgreSQL endpoint")
	}
	if parsed.User == nil || parsed.User.Username() == "" {
		return PostgresEndpoint{}, errors.New("PostgreSQL username is required")
	}
	database, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !identifierPattern.MatchString(database) {
		return PostgresEndpoint{}, errors.New("invalid PostgreSQL database name")
	}
	if !identifierPattern.MatchString(schema) {
		return PostgresEndpoint{}, errors.New("invalid PostgreSQL schema name")
	}
	for key, values := range parsed.Query() {
		if _, allowed := allowedConnectionOptions[key]; !allowed || len(values) != 1 {
			return PostgresEndpoint{}, fmt.Errorf("unsupported PostgreSQL connection option %q", key)
		}
	}
	port := parsed.Port()
	if port == "" {
		port = "5432"
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return PostgresEndpoint{}, errors.New("invalid PostgreSQL port")
	}
	return PostgresEndpoint{URL: parsed.String(), Host: parsed.Hostname(), Port: port, Database: database, Schema: schema}, nil
}

func QuoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

type NamespaceReader interface {
	ReadNamespace(context.Context, PostgresEndpoint) (NamespaceSnapshot, error)
}

type TenantInitializer interface {
	Initialize(context.Context, PostgresEndpoint) error
}

type PreflightService struct {
	Reader      NamespaceReader
	Initializer TenantInitializer
}

func (s PreflightService) Probe(ctx context.Context, endpoint PostgresEndpoint) (NamespaceSnapshot, error) {
	if s.Reader == nil {
		return NamespaceSnapshot{}, errors.New("namespace reader is required")
	}
	snapshot, err := s.Reader.ReadNamespace(ctx, endpoint)
	if isTenantSchemaMissing(err) {
		return NamespaceSnapshot{}, ErrTenantSchemaNotInitialized
	}
	return snapshot, err
}

func isTenantSchemaMissing(err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && (pgError.Code == "42P01" || pgError.Code == "3F000")
}

func (s PreflightService) Initialize(ctx context.Context, endpoint PostgresEndpoint) (NamespaceSnapshot, error) {
	if s.Initializer == nil {
		return NamespaceSnapshot{}, errors.New("tenant initializer is required")
	}
	if err := s.Initializer.Initialize(ctx, endpoint); err != nil {
		return NamespaceSnapshot{}, err
	}
	return s.Probe(ctx, endpoint)
}

// SQLNamespaceReader performs read-only identity discovery. tenant_installations
// is only created by an explicit initializer/migration runner.
type SQLNamespaceReader struct {
	Open func(context.Context, PostgresEndpoint) (*sql.DB, error)
}

func (r SQLNamespaceReader) ReadNamespace(ctx context.Context, endpoint PostgresEndpoint) (NamespaceSnapshot, error) {
	if r.Open == nil {
		return NamespaceSnapshot{}, errors.New("database opener is required")
	}
	schemaURL, err := endpoint.URLWithSchema()
	if err != nil {
		return NamespaceSnapshot{}, err
	}
	endpoint.URL = schemaURL
	db, err := r.Open(ctx, endpoint)
	if err != nil {
		return NamespaceSnapshot{}, err
	}
	defer db.Close()
	var snapshot NamespaceSnapshot
	snapshot.Provider = "postgres"
	err = db.QueryRowContext(ctx, `
		SELECT installation_id::text,
		       (pg_control_system()).system_identifier::text || ':' ||
		         (SELECT oid::text FROM pg_database WHERE datname=current_database()),
		       schema_identity
		FROM tenant_installations
		WHERE singleton_key=1`).Scan(&snapshot.InstallationID, &snapshot.DatabaseIdentity, &snapshot.SchemaIdentity)
	if err != nil {
		return NamespaceSnapshot{}, err
	}
	if snapshot.SchemaIdentity != endpoint.Schema {
		return NamespaceSnapshot{}, fmt.Errorf("tenant schema identity mismatch")
	}
	return snapshot, nil
}
