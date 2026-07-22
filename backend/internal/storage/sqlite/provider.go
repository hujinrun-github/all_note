package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	_ "modernc.org/sqlite"
)

type Provider struct{}

func (p Provider) OpenControl(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	db, err := p.openWithoutMigrations(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := requireSQLiteMigrationTable(ctx, db, "control_schema_migrations", ""); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrControlSchemaNotReady, err)
	}
	if err := verifySQLiteControlMigrations(ctx, db); err != nil {
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
	if err := requireSQLiteMigrationTable(ctx, db, "tenant_schema_migrations", expectedSchemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrTenantSchemaNotReady, err)
	}
	if err := verifySQLiteTenantMigrationChecksum(ctx, db, expectedSchemaVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", storage.ErrTenantSchemaNotReady, err)
	}
	return newStore(db), nil
}

func (p Provider) openWithoutMigrations(ctx context.Context, cfg storage.Config) (*sql.DB, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.SQLitePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func requireSQLiteMigrationTable(ctx context.Context, db *sql.DB, tableName, expectedVersion string) error {
	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, tableName).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s is missing", tableName)
	}
	if expectedVersion == "" {
		return nil
	}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE version = ?`, tableName)
	if err := db.QueryRowContext(ctx, query, expectedVersion).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%s does not contain expected version %s", tableName, expectedVersion)
	}
	return nil
}

func (Provider) Driver() storage.Driver {
	return storage.DriverSQLite
}

func (Provider) Validate(cfg storage.Config) error {
	if strings.TrimSpace(cfg.SQLitePath) == "" {
		return errors.New("FLOWSPACE_SQLITE_PATH is required")
	}
	return storage.ValidateStorageConfig(cfg)
}

func (p Provider) Open(ctx context.Context, cfg storage.Config) (storage.Store, error) {
	if err := p.Validate(cfg); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", cfg.SQLitePath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteSyncColumnsBeforeSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := initializeLegacySchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := repository.RunLegacySQLiteMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteSyncSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureRecurrenceSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteAuthSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteMobileSyncSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteEventProjectSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteCalendarProjectSourcesSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteNativeSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteTranscriptionJobSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSQLiteVoiceAudioCleanupSchema(ctx, db); err != nil {
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

func initializeLegacySchema(db *sql.DB) error {
	schemaPath, err := legacySchemaPath()
	if err != nil {
		return err
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read SQLite schema: %w", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return fmt.Errorf("initialize SQLite schema: %w", err)
	}
	return nil
}

func legacySchemaPath() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve sqlite provider path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", "db", "schema.sql")), nil
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

func (s *store) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		FullTextSearch: true,
		PrefixSearch:   true,
	}
}

func (s *store) Transact(ctx context.Context, fn func(storage.Store) error) error {
	if fn == nil {
		return errors.New("sqlite transaction callback is nil")
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

func (s *store) Recurrence() storage.RecurrenceRepository {
	return recurrenceRepository{db: s.db}
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

func (s *storeTx) Recurrence() storage.RecurrenceRepository {
	return recurrenceRepository{db: s.tx}
}

func (s *storeTx) Tasks() storage.TaskRepository {
	return taskRepository{db: s.tx}
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
