package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/contracttest"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestPostgresTaskDomainV2SchemaContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2SchemaSuite(t, db, contracttest.TaskDomainV2Postgres)
}

func TestPostgresTaskDomainV2ProjectContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_project_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2ProjectSuite(t, contracttest.TaskDomainV2ProjectFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: NewTenantWriter(cfg),
		NewReader: func(workspaceID string) taskdomain.ProjectReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestPostgresTaskDomainV2RoadmapContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_roadmap_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2RoadmapSuite(t, contracttest.TaskDomainV2RoadmapFixture{DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: NewTenantWriter(cfg), NewReader: func(w string) taskdomain.RoadmapReader {
		return newTaskDomainV2ProjectReader(db, w).(taskdomain.RoadmapReader)
	}})
}

func TestPostgresTaskDomainV2AggregateContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_aggregate_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2AggregateSuite(t, contracttest.TaskDomainV2AggregateFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: NewTenantWriter(cfg),
	})
}

func TestPostgresTaskDomainV2StateContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_state_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2StateSuite(t, contracttest.TaskDomainV2StateFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: NewTenantWriter(cfg),
	})
}

func TestPostgresTaskDomainV2QueryContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_query_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	contracttest.RunTaskDomainV2QuerySuite(t, contracttest.TaskDomainV2QueryFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: NewTenantWriter(cfg),
		NewReader: func(workspaceID string) taskdomain.TaskDomainReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestPostgresTaskDomainV2ScheduleContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_schedule_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2ScheduleSuite(t, contracttest.TaskDomainV2ScheduleFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: writer, Fencer: writer,
		NewStateReader: func(workspaceID string) taskdomain.ScheduleCommandStateReader {
			return newTaskDomainV2ProjectReader(db, workspaceID).(taskdomain.ScheduleCommandStateReader)
		},
	})
}

func TestPostgresTaskDomainV2ProjectCommandContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_project_command_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2ProjectCommandSuite(t, contracttest.TaskDomainV2ProjectCommandFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: writer, Fencer: writer,
		Reader: func(workspaceID string) taskdomain.ProjectReader {
			return newTaskDomainV2ProjectReader(db, workspaceID)
		},
	})
}

func TestPostgresTaskDomainV2CompletionContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_completion_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2CompletionSuite(t, contracttest.TaskDomainV2CompletionFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: writer, Fencer: writer, ScheduleFencer: writer,
	})
}

func TestPostgresTaskDomainV2GeneratorContract(t *testing.T) {
	rawURL := createPostgresTestSchema(t, fmt.Sprintf("fs_test_task_domain_v2_generator_%d", time.Now().UnixNano()))
	cfg := storage.Config{Env: "test", Driver: storage.DriverPostgres, URL: rawURL}
	if err := (Provider{}).MigrateTenant(context.Background(), cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	db, err := sql.Open("pgx", rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	writer := NewTenantWriter(cfg)
	contracttest.RunTaskDomainV2GeneratorSuite(t, contracttest.TaskDomainV2GeneratorFixture{
		DB: db, Dialect: contracttest.TaskDomainV2Postgres, Writer: writer, Fencer: writer, ScheduleFencer: writer,
	})
}

func TestPostgresStoreContract(t *testing.T) {
	contracttest.RunStoreSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresMobileSyncNoteContract(t *testing.T) {
	contracttest.RunMobileSyncNoteSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		schema := fmt.Sprintf("fs_test_mobile_sync_contract_%d", time.Now().UnixNano())
		// Each contract case starts from a fresh schema and applies the full
		// migration history. Keep the setup deadline above normal remote database
		// variance now that the tenant migration set has grown.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresTranscriptionJobContract(t *testing.T) {
	contracttest.RunTranscriptionJobSuite(t, func(t *testing.T) storage.Store {
		t.Helper()
		schema := fmt.Sprintf("fs_test_transcription_job_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env: "test", Driver: storage.DriverPostgres, URL: createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresAuthContract(t *testing.T) {
	contracttest.RunAuthContractTests(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_auth_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresNoteSearchContract(t *testing.T) {
	contracttest.RunNoteSearchSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_notes_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresNoteProjectLinksContract(t *testing.T) {
	contracttest.RunNoteProjectLinksSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_note_project_links_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresTaskContract(t *testing.T) {
	contracttest.RunTaskSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_tasks_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresEventInboxContract(t *testing.T) {
	contracttest.RunEventInboxSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_events_inbox_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresRoadmapContract(t *testing.T) {
	contracttest.RunRoadmapSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_roadmaps_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresSyncContract(t *testing.T) {
	contracttest.RunSyncSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_sync_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresRecurrenceContract(t *testing.T) {
	contracttest.RunRecurrenceSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_recurrence_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresSyncBindingContract(t *testing.T) {
	contracttest.RunSyncBindingSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_sync_binding_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresWorkspaceIsolationContract(t *testing.T) {
	contracttest.RunWorkspaceIsolationSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_workspace_isolation_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}

func TestPostgresCalendarProjectSourcesContract(t *testing.T) {
	contracttest.RunCalendarProjectSourcesSuite(t, func(t *testing.T) storage.Store {
		t.Helper()

		schema := fmt.Sprintf("fs_test_calendar_sources_contract_%d", time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		store, err := (Provider{}).Open(ctx, storage.Config{
			Env:    "test",
			Driver: storage.DriverPostgres,
			URL:    createPostgresTestSchema(t, schema),
		})
		if err != nil {
			t.Fatalf("open postgres store: %v", err)
		}
		return store
	})
}
