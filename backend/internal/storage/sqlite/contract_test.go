package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestSQLiteTaskDomainV2SchemaContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2SchemaSuite(t, db, contracttest.TaskDomainV2SQLite)
}

func TestSQLiteTaskDomainV2ProjectContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-project.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2ProjectSuite(t, contracttest.TaskDomainV2ProjectFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: NewTenantWriter(cfg),
		NewReader: func(workspaceID string) taskdomain.ProjectReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestSQLiteTaskDomainV2RoadmapContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-roadmap.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2RoadmapSuite(t, contracttest.TaskDomainV2RoadmapFixture{DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: NewTenantWriter(cfg), NewReader: func(w string) taskdomain.RoadmapReader {
		return newTaskDomainV2ProjectReader(db, w).(taskdomain.RoadmapReader)
	}})
}

func TestSQLiteTaskDomainV2AggregateContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-aggregate.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2AggregateSuite(t, contracttest.TaskDomainV2AggregateFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: NewTenantWriter(cfg),
	})
}

func TestSQLiteTaskDomainV2StateContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-state.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2StateSuite(t, contracttest.TaskDomainV2StateFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: NewTenantWriter(cfg),
	})
}

func TestSQLiteTaskDomainV2QueryContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-query.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2QuerySuite(t, contracttest.TaskDomainV2QueryFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: NewTenantWriter(cfg),
		NewReader: func(workspaceID string) taskdomain.TaskDomainReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestSQLiteTaskDomainV2ScheduleContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-schedule.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2ScheduleSuite(t, contracttest.TaskDomainV2ScheduleFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: writer, Fencer: writer,
		NewStateReader: func(workspaceID string) taskdomain.ScheduleCommandStateReader {
			return newTaskDomainV2ProjectReader(db, workspaceID).(taskdomain.ScheduleCommandStateReader)
		},
	})
}

func TestSQLiteTaskDomainV2ProjectCommandContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-project-command.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2ProjectCommandSuite(t, contracttest.TaskDomainV2ProjectCommandFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: writer, Fencer: writer,
		Reader: func(workspaceID string) taskdomain.ProjectReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestSQLiteTaskDomainV2CompletionContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-completion.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2CompletionSuite(t, contracttest.TaskDomainV2CompletionFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: writer, Fencer: writer, ScheduleFencer: writer,
	})
}

func TestSQLiteTaskDomainV2GeneratorContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flowspace.task-domain-v2-generator.test.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2GeneratorSuite(t, contracttest.TaskDomainV2GeneratorFixture{
		DB: db, Dialect: contracttest.TaskDomainV2SQLite, Writer: writer, Fencer: writer, ScheduleFencer: writer,
	})
}

func TestSQLiteStoreContract(t *testing.T) {
	contracttest.RunStoreSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteMobileSyncNoteContract(t *testing.T) {
	contracttest.RunMobileSyncNoteSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.mobile-sync.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteTranscriptionJobContract(t *testing.T) {
	contracttest.RunTranscriptionJobSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.transcription-job.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteAuthContract(t *testing.T) {
	contracttest.RunAuthContractTests(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteNoteSearchContract(t *testing.T) {
	contracttest.RunNoteSearchSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteNoteProjectLinksContract(t *testing.T) {
	contracttest.RunNoteProjectLinksSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteTaskContract(t *testing.T) {
	contracttest.RunTaskSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteEventInboxContract(t *testing.T) {
	contracttest.RunEventInboxSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteRoadmapContract(t *testing.T) {
	contracttest.RunRoadmapSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteSyncContract(t *testing.T) {
	contracttest.RunSyncSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteRecurrenceContract(t *testing.T) {
	contracttest.RunRecurrenceSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteSyncBindingContract(t *testing.T) {
	contracttest.RunSyncBindingSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteWorkspaceIsolationContract(t *testing.T) {
	contracttest.RunWorkspaceIsolationSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}

func TestSQLiteCalendarProjectSourcesContract(t *testing.T) {
	contracttest.RunCalendarProjectSourcesSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		store, err := (Provider{}).Open(context.Background(), storage.Config{
			Env:        "test",
			Driver:     storage.DriverSQLite,
			SQLitePath: filepath.Join(t.TempDir(), "flowspace.test.db"),
		})
		if err != nil {
			t.Fatalf("open sqlite store: %v", err)
		}
		return store
	})
}
