package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Provider struct{}

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

	db, err := sql.Open("pgx", cfg.URL)
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

func (s *storeTx) Events() storage.EventRepository {
	return eventRepository{db: s.tx}
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
