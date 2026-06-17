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
	db, err := sql.Open("sqlite", cfg.SQLitePath+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
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

func (s *store) Folders() storage.FolderRepository {
	return folderRepository{db: s.db}
}

func (s *store) Notes() storage.NoteRepository {
	return noteRepository{db: s.db}
}

func (s *store) Tasks() storage.TaskRepository {
	panic("sqlite task repository is not implemented yet")
}

func (s *store) Events() storage.EventRepository {
	panic("sqlite event repository is not implemented yet")
}

func (s *store) Inbox() storage.InboxRepository {
	panic("sqlite inbox repository is not implemented yet")
}

func (s *store) Roadmaps() storage.RoadmapRepository {
	panic("sqlite roadmap repository is not implemented yet")
}

func (s *store) Sync() storage.SyncRepository {
	panic("sqlite sync repository is not implemented yet")
}

func (s *store) Search() storage.SearchRepository {
	return searchRepository{db: s.db}
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
