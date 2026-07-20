package tenantmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"

	"github.com/hujinrun/flowspace/internal/outbound"
	"github.com/hujinrun/flowspace/internal/storage"
	storagepostgres "github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

var (
	ErrMaintenanceDatabaseRequired = errors.New("maintenance database is required to create the target database")
	ErrDatabaseCreateDenied        = errors.New("target database does not exist and could not be created")
)

func (e PostgresEndpoint) URLForDatabase(database string) (string, error) {
	if !identifierPattern.MatchString(database) {
		return "", errors.New("invalid maintenance database name")
	}
	parsed, err := url.Parse(e.URL)
	if err != nil {
		return "", errors.New("invalid PostgreSQL endpoint")
	}
	parsed.Path = "/" + database
	parsed.RawPath = ""
	return parsed.String(), nil
}

func (e PostgresEndpoint) URLWithSchema() (string, error) {
	if !identifierPattern.MatchString(e.Schema) {
		return "", errors.New("invalid PostgreSQL schema name")
	}
	parsed, err := url.Parse(e.URL)
	if err != nil {
		return "", errors.New("invalid PostgreSQL endpoint")
	}
	query := parsed.Query()
	query.Set("search_path", e.Schema)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func IsDatabaseMissing(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "3D000"
}

func IsDuplicateDatabase(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "42P04"
}

func initializationError(operation, _ string) error {
	return fmt.Errorf("%s failed", operation)
}

type PostgresInitializer struct {
	MaintenanceDatabase string
	Open                func(string) (*sql.DB, error)
	Migrate             func(context.Context, PostgresEndpoint) error
}

func NewPostgresInitializer(dialer *outbound.Dialer, maintenanceDatabase, environment string) (PostgresInitializer, error) {
	if dialer == nil {
		return PostgresInitializer{}, errors.New("safe outbound dialer is required")
	}
	opener := func(rawURL string) (*sql.DB, error) {
		config, err := pgx.ParseConfig(rawURL)
		if err != nil {
			return nil, errors.New("invalid PostgreSQL endpoint")
		}
		config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		}
		return stdlib.OpenDB(*config), nil
	}
	provider := storagepostgres.Provider{}
	return PostgresInitializer{
		MaintenanceDatabase: maintenanceDatabase,
		Open:                opener,
		Migrate: func(ctx context.Context, endpoint PostgresEndpoint) error {
			return provider.MigrateTenant(ctx, storage.Config{Env: environment, Driver: storage.DriverPostgres, URL: endpoint.URL, Name: endpoint.Database})
		},
	}, nil
}

func (i PostgresInitializer) Initialize(ctx context.Context, endpoint PostgresEndpoint) error {
	open := i.Open
	if open == nil {
		return errors.New("safe PostgreSQL opener is required")
	}
	target, err := open(endpoint.URL)
	if err != nil {
		return initializationError("open target database", endpoint.URL)
	}
	err = target.PingContext(ctx)
	if err != nil {
		_ = target.Close()
		if !IsDatabaseMissing(err) {
			return initializationError("connect target database", endpoint.URL)
		}
		if err := i.createDatabase(ctx, open, endpoint); err != nil {
			return err
		}
		target, err = open(endpoint.URL)
		if err != nil {
			return initializationError("open created database", endpoint.URL)
		}
		if err := target.PingContext(ctx); err != nil {
			_ = target.Close()
			return initializationError("connect created database", endpoint.URL)
		}
	}
	defer target.Close()

	if _, err := target.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+QuoteIdentifier(endpoint.Schema)); err != nil {
		return initializationError("create tenant schema", endpoint.URL)
	}
	if i.Migrate == nil {
		return errors.New("tenant migration runner is required")
	}
	schemaURL, err := endpoint.URLWithSchema()
	if err != nil {
		return err
	}
	endpoint.URL = schemaURL
	if err := i.Migrate(ctx, endpoint); err != nil {
		return initializationError("migrate tenant schema", endpoint.URL)
	}
	return nil
}

func (i PostgresInitializer) createDatabase(ctx context.Context, open func(string) (*sql.DB, error), endpoint PostgresEndpoint) error {
	if i.MaintenanceDatabase == "" {
		return ErrMaintenanceDatabaseRequired
	}
	maintenanceURL, err := endpoint.URLForDatabase(i.MaintenanceDatabase)
	if err != nil {
		return err
	}
	maintenance, err := open(maintenanceURL)
	if err != nil {
		return ErrDatabaseCreateDenied
	}
	defer maintenance.Close()
	if err := maintenance.PingContext(ctx); err != nil {
		return ErrDatabaseCreateDenied
	}
	_, err = maintenance.ExecContext(ctx, `CREATE DATABASE `+QuoteIdentifier(endpoint.Database))
	if err != nil && !IsDuplicateDatabase(err) {
		return ErrDatabaseCreateDenied
	}
	return nil
}
