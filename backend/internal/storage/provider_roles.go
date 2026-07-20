package storage

import (
	"context"
	"database/sql"
	"errors"
)

// SQLStore exposes the connection owned by a SQL-backed control store.
// It is intentionally separate from Store so tenant repositories do not
// accidentally depend on raw SQL access.
type SQLStore interface {
	Store
	SQLDB() *sql.DB
}

var (
	ErrControlSchemaNotReady = errors.New("control schema is not ready")
	ErrTenantSchemaNotReady  = errors.New("tenant schema is not ready")
)

type AdoptManifest struct {
	ID       string
	Checksum string
}

type ControlProvider interface {
	OpenControl(context.Context, Config) (Store, error)
	MigrateControl(context.Context, Config) error
}

type TenantProvider interface {
	OpenTenant(context.Context, Config, string) (Store, error)
}

type TenantMaintenanceProvider interface {
	MigrateTenant(context.Context, Config) error
	AdoptExistingTenant(context.Context, Config, AdoptManifest) error
}
