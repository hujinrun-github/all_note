package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DialContextFunc is invoked by pgx for every new physical PostgreSQL
// connection. A user-configurable tenant endpoint must use a Provider created
// by NewProviderWithDialContext so hostname resolution and address policy are
// enforced at the same point as the network connection.
type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type Provider struct {
	dialContext DialContextFunc
}

// NewProviderWithDialContext creates a PostgreSQL provider for untrusted,
// user-configurable endpoints. The zero-value Provider remains available for
// trusted deployment-owned control and platform databases.
func NewProviderWithDialContext(dialContext DialContextFunc) (Provider, error) {
	if dialContext == nil {
		return Provider{}, errors.New("PostgreSQL dial context is required")
	}
	return Provider{dialContext: dialContext}, nil
}

func (p Provider) OpenControl(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := requirePostgresMigrationTable(ctx, db, "control_schema_migrations", ""); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrControlSchemaNotReady, err)
	}
	if err := verifyPostgresControlMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrControlSchemaNotReady, err)
	}
	return newStore(db), nil
}

func (p Provider) OpenTenant(ctx context.Context, cfg storage.Config, expectedSchemaVersion string) (storage.Store, error) {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := requirePostgresMigrationTable(ctx, db, "tenant_schema_migrations", expectedSchemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrTenantSchemaNotReady, err)
	}
	if err := verifyPostgresTenantMigrationChecksum(ctx, db, expectedSchemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrTenantSchemaNotReady, err)
	}
	return newStore(db), nil
}

func (Provider) AdoptExistingTenant(context.Context, storage.Config, storage.AdoptManifest) error {
	return storage.ErrNotImplemented
}

func (p Provider) openWithoutMigrations(ctx context.Context, cfg storage.Config) (*sql.DB, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}
	db, err := p.openDatabase(cfg.URL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (p Provider) openDatabase(databaseURL string) (*sql.DB, error) {
	if p.dialContext == nil {
		return sql.Open("pgx", databaseURL)
	}
	connConfig, err := p.connectionConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*connConfig), nil
}

func (p Provider) connectionConfig(databaseURL string) (*pgx.ConnConfig, error) {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	// pgx normally resolves DNS before DialFunc. Returning the unchanged host
	// keeps resolution inside the injected dial-and-validate operation on every
	// connection, while pgx's TLS config retains the original hostname for SNI
	// and certificate verification.
	connConfig.LookupFunc = func(_ context.Context, host string) ([]string, error) {
		return []string{host}, nil
	}
	connConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		return p.dialContext(ctx, network, address)
	}
	return connConfig, nil
}

func requirePostgresMigrationTable(ctx context.Context, db *sql.DB, tableName, expectedVersion string) error {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, tableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%s is missing", tableName)
	}
	if expectedVersion == "" {
		return nil
	}
	var count int
	var query string
	switch tableName {
	case "control_schema_migrations":
		query = `SELECT COUNT(*) FROM control_schema_migrations WHERE version = $1`
	case "tenant_schema_migrations":
		query = `SELECT COUNT(*) FROM tenant_schema_migrations WHERE version = $1`
	default:
		return fmt.Errorf("unsupported migration table %q", tableName)
	}
	if err := db.QueryRowContext(ctx, query, expectedVersion).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s does not contain expected version %s", tableName, expectedVersion)
	}
	return nil
}

func (Provider) Driver() storage.Driver {
	return storage.DriverPostgres
}

func (Provider) Validate(cfg storage.Config) error {
	if strings.TrimSpace(cfg.URL) == "" {
		return errors.New("FLOWSPACE_DATABASE_URL is required")
	}
	return storage.ValidateStorageConfig(cfg)
}

func (p Provider) Open(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}

	db, err := p.openDatabase(cfg.URL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := RunPostgresMigrationsContext(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return newStore(db), nil
}

func (p Provider) Migrate(ctx context.Context, cfg storage.Config) error {
	store, err := p.Open(ctx, cfg)
	if err != nil {
		return err
	}
	return store.Close()
}

type store struct {
	db *sql.DB
}

func (s *store) SQLDB() *sql.DB { return s.db }

func newStore(db *sql.DB) storage.Store {
	return &store{db: db}
}

func (s *store) Close() error {
	return s.db.Close()
}

func (s *store) Health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *store) FinalizeAuthSchema(ctx context.Context) error {
	return runMultiUserAuthFinalizer(ctx, s.db)
}

func (s *store) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		FullTextSearch: true,
		PrefixSearch:   true,
		TrigramSearch:  true,
		JSONObjects:    true,
		ArrayColumns:   true,
		TimeRanges:     true,
		AdvisoryLocks:  true,
	}
}

func (s *store) Transact(ctx context.Context, fn func(storage.Store) error) error {
	if fn == nil {
		return errors.New("postgres transaction callback is nil")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if !committed {
				_ = tx.Rollback()
			}
			panic(recovered)
		}
		if !committed {
			_ = tx.Rollback()
		}
	}()

	txStore := &storeTx{store: s, tx: tx}
	if err := fn(txStore); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *store) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return s.db.ExecContext(ctx, query, args...)
}

func (s *store) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, query, args...)
}

func (s *store) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return s.db.QueryRowContext(ctx, query, args...)
}

func (s *store) Folders() storage.FolderRepository {
	return folderRepository{db: s.db}
}

func (s *store) Notes() storage.NoteRepository {
	return noteRepository{db: s.db}
}

func (s *store) Tasks() storage.TaskRepository {
	return taskRepository{db: s.db}
}

func (s *store) Recurrence() storage.RecurrenceRepository {
	return recurrenceRepository{db: s.db}
}

func (s *store) Events() storage.EventRepository {
	return eventRepository{db: s.db}
}

func (s *store) Calendar() storage.CalendarRepository {
	return calendarRepository{db: s.db}
}

func (s *store) Inbox() storage.InboxRepository {
	return inboxRepository{db: s.db}
}

func (s *store) Roadmaps() storage.RoadmapRepository {
	return roadmapRepository{db: s.db}
}

func (s *store) Sync() storage.SyncRepository {
	return syncRepository{db: s.db}
}

func (s *store) Search() storage.SearchRepository {
	return searchRepository{db: s.db}
}

func (s *store) Auth() storage.AuthRepository {
	return authRepository{db: s.db}
}

func (s *store) MobileSync() storage.MobileSyncRepository {
	return mobileSyncRepository{db: s.db}
}

func (s *store) MobileSyncPublisher() storage.MobileSyncPublisherRepository {
	return mobileSyncPublisherRepository{db: s.db}
}

func (s *store) TranscriptionJobs() storage.TranscriptionJobRepository {
	return transcriptionJobRepository{db: s.db}
}

func (s *store) TranscriptionJobWorker() storage.TranscriptionJobWorkerRepository {
	return transcriptionJobWorkerRepository{db: s.db}
}

func (s *store) VoiceAudioCleanup() storage.VoiceAudioCleanupRepository {
	return voiceAudioCleanupRepository{db: s.db}
}

func (s *store) WatchDevices() storage.WatchDeviceRepository {
	return watchDeviceRepository{db: s.db}
}

func (s *store) VoiceNotes() storage.VoiceNoteRepository {
	return voiceNoteRepository{db: s.db}
}

type storeTx struct {
	*store
	tx *sql.Tx
}

func (s *storeTx) Folders() storage.FolderRepository {
	return folderRepository{db: s.tx}
}

func (s *storeTx) Notes() storage.NoteRepository {
	return noteRepository{db: s.tx}
}

func (s *storeTx) Search() storage.SearchRepository {
	return searchRepository{db: s.tx}
}

func (s *storeTx) Tasks() storage.TaskRepository {
	return taskRepository{db: s.tx}
}

func (s *storeTx) Recurrence() storage.RecurrenceRepository {
	return recurrenceRepository{db: s.tx}
}

func (s *storeTx) Events() storage.EventRepository {
	return eventRepository{db: s.tx}
}

func (s *storeTx) Calendar() storage.CalendarRepository {
	return calendarRepository{db: s.tx}
}

func (s *storeTx) Inbox() storage.InboxRepository {
	return inboxRepository{db: s.tx}
}

func (s *storeTx) Roadmaps() storage.RoadmapRepository {
	return roadmapRepository{db: s.tx}
}

func (s *storeTx) Sync() storage.SyncRepository {
	return syncRepository{db: s.tx}
}

func (s *storeTx) Auth() storage.AuthRepository {
	return authRepository{db: s.tx}
}

func (s *storeTx) MobileSync() storage.MobileSyncRepository {
	return mobileSyncRepository{db: s.tx}
}

func (s *storeTx) TranscriptionJobs() storage.TranscriptionJobRepository {
	return transcriptionJobRepository{db: s.tx}
}

func (s *storeTx) VoiceAudioCleanup() storage.VoiceAudioCleanupRepository {
	return voiceAudioCleanupRepository{db: s.tx}
}

func (s *storeTx) WatchDevices() storage.WatchDeviceRepository {
	return watchDeviceRepository{db: s.tx}
}

func (s *storeTx) VoiceNotes() storage.VoiceNoteRepository {
	return voiceNoteRepository{db: s.tx}
}

func (s *storeTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return s.tx.ExecContext(ctx, query, args...)
}

func (s *storeTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return s.tx.QueryContext(ctx, query, args...)
}

func (s *storeTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return s.tx.QueryRowContext(ctx, query, args...)
}
