package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallLegacyOutboxTriggersFreshV2DoesNotInspectOptionalLegacyTables(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	var beforeObjects int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&beforeObjects); err != nil {
		t.Fatalf("count SQLite objects before fresh-v2 install: %v", err)
	}

	if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceFreshV2); err != nil {
		t.Fatalf("InstallLegacyOutboxTriggers(fresh-v2 empty database): %v", err)
	}
	if got := countInstalledLegacyOutboxTriggers(t, db); got != 0 {
		t.Fatalf("installed trigger count = %d, want 0", got)
	}
	var afterObjects int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&afterObjects); err != nil {
		t.Fatalf("count SQLite objects after fresh-v2 install: %v", err)
	}
	if afterObjects != beforeObjects {
		t.Fatalf("fresh-v2 install changed SQLite schema objects: before=%d after=%d", beforeObjects, afterObjects)
	}
}

func TestInstallLegacyOutboxTriggersRejectsInvalidArguments(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	tests := []struct {
		name    string
		ctx     context.Context
		db      *sql.DB
		dialect Dialect
		mode    TaskDomainSourceMode
	}{
		{name: "nil context", db: db, dialect: DialectSQLite, mode: TaskDomainSourceFreshV2},
		{name: "nil database", ctx: context.Background(), dialect: DialectSQLite, mode: TaskDomainSourceFreshV2},
		{name: "unknown dialect", ctx: context.Background(), db: db, dialect: Dialect("mysql"), mode: TaskDomainSourceFreshV2},
		{name: "unknown source mode", ctx: context.Background(), db: db, dialect: DialectSQLite, mode: TaskDomainSourceMode("mixed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := InstallLegacyOutboxTriggers(test.ctx, test.db, test.dialect, test.mode)
			if !errors.Is(err, ErrInvalidLegacyOutboxInstallerInput) {
				t.Fatalf("error = %v, want ErrInvalidLegacyOutboxInstallerInput", err)
			}
		})
	}
}

func TestInstallLegacyOutboxTriggersSQLiteBlocksIncompleteInventoryWithoutPartialDDL(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
		code   TriggerPlanBlockCode
	}{
		{
			name: "missing table",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExecSQLiteOutbox(t, db, `DROP TABLE task_occurrences`)
			},
			code: TriggerPlanMissingTable,
		},
		{
			name: "missing canonical column",
			mutate: func(t *testing.T, db *sql.DB) {
				mustExecSQLiteOutbox(t, db, `DROP TABLE events`)
				mustExecSQLiteOutbox(t, db, `CREATE TABLE events (
					end_time TEXT NOT NULL, id TEXT NOT NULL, is_all_day INTEGER NOT NULL, kind TEXT,
					location TEXT, note_id TEXT, notes TEXT, project_id TEXT,
					title TEXT NOT NULL, updated_at TEXT NOT NULL, workspace_id TEXT NOT NULL
				)`)
			},
			code: TriggerPlanMissingColumn,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteOutboxTestDB(t)
			createSQLiteOutboxFixtureSchema(t, db)
			test.mutate(t, db)

			err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace)
			var blocked *TriggerPlanBlock
			if !errors.As(err, &blocked) {
				t.Fatalf("error = %v, want TriggerPlanBlock", err)
			}
			foundCode := false
			for _, reason := range blocked.Reasons {
				if reason.Code == test.code {
					foundCode = true
					break
				}
			}
			if !foundCode {
				t.Fatalf("block reasons = %#v, want code %q", blocked.Reasons, test.code)
			}
			if got := countInstalledLegacyOutboxTriggers(t, db); got != 0 {
				t.Fatalf("installed trigger count after blocked inventory = %d, want 0", got)
			}
		})
	}
}

func TestInstallLegacyOutboxTriggersSQLiteInstallsCompletePlanAndIsRepeatable(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace); err != nil {
			t.Fatalf("InstallLegacyOutboxTriggers attempt %d: %v", attempt, err)
		}
		if got := countInstalledLegacyOutboxTriggers(t, db); got != 15 {
			t.Fatalf("installed trigger count after attempt %d = %d, want 15", attempt, got)
		}
	}
}

func TestInstallLegacyOutboxTriggersSQLiteSeedsExistingSourcesWithoutResettingVersions(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)

	const workspaceID = "workspace-baseline"
	mustExecSQLiteOutbox(t, db, `INSERT INTO workspace_task_domain_state
		(workspace_id, accept_legacy_writes) VALUES (?, 1)`, workspaceID)
	mustExecSQLiteOutbox(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES (?, 'project-baseline', 'Baseline', 'personal')`, workspaceID)
	mustExecSQLiteOutbox(t, db, `INSERT INTO tasks
		(workspace_id, id, done, priority, sort_order, title, updated_at)
		VALUES (?, 'task-baseline', 0, 1, 0, 'Task baseline', CURRENT_TIMESTAMP)`, workspaceID)
	mustExecSQLiteOutbox(t, db, `INSERT INTO task_recurrence_rules
		(workspace_id, task_id, enabled, frequency, "interval", start_date, timezone, updated_at)
		VALUES (?, 'task-baseline', 1, 'weekly', 1, '2026-07-22', 'Asia/Shanghai', CURRENT_TIMESTAMP)`, workspaceID)
	mustExecSQLiteOutbox(t, db, `INSERT INTO task_occurrences
		(workspace_id, task_id, occurrence_date, status, updated_at)
		VALUES (?, 'task-baseline', '2026-07-23', 'pending', CURRENT_TIMESTAMP)`, workspaceID)
	mustExecSQLiteOutbox(t, db, `INSERT INTO events
		(workspace_id, id, title, start_time, end_time, is_all_day, updated_at)
		VALUES (?, 'event-baseline', 'Event baseline', '2026-07-24T01:00:00Z', '2026-07-24T02:00:00Z', 0, CURRENT_TIMESTAMP)`, workspaceID)

	if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace); err != nil {
		t.Fatalf("InstallLegacyOutboxTriggers initial: %v", err)
	}
	occurrenceID := `["task-baseline","2026-07-23"]`
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityProject, "project-baseline", 1, false)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityTask, "task-baseline", 1, false)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityRule, "task-baseline", 1, false)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityOccurrence, occurrenceID, 1, false)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityEvent, "event-baseline", 1, false)

	var outboxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_domain_legacy_outbox`).Scan(&outboxCount); err != nil {
		t.Fatalf("count baseline outbox events: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("baseline seeding emitted %d outbox events, want 0", outboxCount)
	}

	mustExecSQLiteOutbox(t, db, `UPDATE task_projects SET name='Advanced'
		WHERE workspace_id=? AND id='project-baseline'`, workspaceID)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityProject, "project-baseline", 2, false)

	if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace); err != nil {
		t.Fatalf("InstallLegacyOutboxTriggers repeated: %v", err)
	}
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityProject, "project-baseline", 2, false)
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_domain_legacy_outbox`).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox events after repeated install: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("repeated baseline seeding changed outbox count to %d, want 1", outboxCount)
	}
}

func TestInstallLegacyOutboxTriggersSQLiteCanonicalizesPhysicalDueAndFreezesRoadmaps(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)
	mustExecSQLiteOutbox(t, db, `ALTER TABLE tasks RENAME COLUMN due_at TO due`)
	const workspaceID = "workspace-alias-freeze"
	mustExecSQLiteOutbox(t, db, `INSERT INTO workspace_task_domain_state(workspace_id,accept_legacy_writes,migration_state) VALUES(?,1,'idle')`, workspaceID)
	if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace); err != nil {
		t.Fatalf("install physical due/freeze triggers: %v", err)
	}
	mustExecSQLiteOutbox(t, db, `INSERT INTO tasks(workspace_id,id,done,due,priority,sort_order,title,updated_at) VALUES(?,'task-due',0,'2026-07-25T01:02:03Z',1,0,'Due',CURRENT_TIMESTAMP)`, workspaceID)
	var canonicalDue string
	if err := db.QueryRow(`SELECT json_extract(row_image,'$.due_at') FROM task_domain_legacy_outbox WHERE workspace_id=? AND entity_kind='task' ORDER BY sequence DESC LIMIT 1`, workspaceID).Scan(&canonicalDue); err != nil {
		t.Fatalf("read canonical due_at image: %v", err)
	}
	if canonicalDue != "2026-07-25T01:02:03Z" {
		t.Fatalf("canonical due_at=%q", canonicalDue)
	}

	mustExecSQLiteOutbox(t, db, `INSERT INTO learning_roadmaps(workspace_id,id) VALUES(?,'roadmap-1')`, workspaceID)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityRoadmap, "roadmap-1", 1, false)
	mustExecSQLiteOutbox(t, db, `UPDATE workspace_task_domain_state SET migration_state='backfilling' WHERE workspace_id=?`, workspaceID)
	if _, err := db.Exec(`UPDATE learning_roadmaps SET id='roadmap-changed' WHERE workspace_id=? AND id='roadmap-1'`, workspaceID); err == nil || !strings.Contains(err.Error(), "legacy_roadmap_frozen") {
		t.Fatalf("roadmap mutation during backfill error=%v", err)
	}
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityRoadmap, "roadmap-1", 1, false)
}

func TestInstallLegacyOutboxTriggersSQLiteRollsBackTriggersWhenBaselineSeedFails(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)
	mustExecSQLiteOutbox(t, db, `DROP TABLE legacy_task_domain_entity_versions`)
	mustExecSQLiteOutbox(t, db, `CREATE TABLE legacy_task_domain_entity_versions (
		workspace_id TEXT NOT NULL,
		entity_kind TEXT NOT NULL,
		entity_id TEXT NOT NULL,
		logical_version INTEGER NOT NULL CHECK (logical_version > 1),
		deleted INTEGER NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (workspace_id, entity_kind, entity_id)
	)`)
	mustExecSQLiteOutbox(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES ('workspace-seed-failure', 'project', 'Project', 'personal')`)

	err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace)
	if err == nil || !errors.Is(err, ErrLegacyOutboxInstallation) {
		t.Fatalf("error = %v, want ErrLegacyOutboxInstallation", err)
	}
	if got := countInstalledLegacyOutboxTriggers(t, db); got != 0 {
		t.Fatalf("installed trigger count after rolled-back seed = %d, want 0", got)
	}
	var ledgerCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM legacy_task_domain_entity_versions`).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger after rolled-back seed: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("ledger rows after rolled-back seed = %d, want 0", ledgerCount)
	}
}

func TestInstallLegacyOutboxTriggersSQLiteSerializesConcurrentLegacyWriteWithBaseline(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "outbox-install-race.db")
	installerDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open installer SQLite database: %v", err)
	}
	writerDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		_ = installerDB.Close()
		t.Fatalf("open writer SQLite database: %v", err)
	}
	installerDB.SetMaxOpenConns(1)
	writerDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = writerDB.Close()
		_ = installerDB.Close()
	})
	for _, db := range []*sql.DB{installerDB, writerDB} {
		if _, err := db.Exec(`PRAGMA busy_timeout = 10000`); err != nil {
			t.Fatalf("set SQLite busy timeout: %v", err)
		}
	}
	createSQLiteOutboxFixtureSchema(t, installerDB)
	const workspaceID = "workspace-install-race"
	mustExecSQLiteOutbox(t, installerDB, `INSERT INTO workspace_task_domain_state
		(workspace_id, accept_legacy_writes) VALUES (?, 1)`, workspaceID)

	start := make(chan struct{})
	installResult := make(chan error, 1)
	writeResult := make(chan error, 1)
	go func() {
		<-start
		installResult <- InstallLegacyOutboxTriggers(
			context.Background(), installerDB, DialectSQLite, TaskDomainSourceLegacyWorkspace,
		)
	}()
	go func() {
		<-start
		_, writeErr := writerDB.Exec(`INSERT INTO task_occurrences
			(workspace_id, task_id, occurrence_date, status, updated_at)
			VALUES (?, 'task-race', '2026-07-25', 'pending', CURRENT_TIMESTAMP)`, workspaceID)
		writeResult <- writeErr
	}()
	close(start)
	if err := <-installResult; err != nil {
		t.Fatalf("concurrent trigger installation: %v", err)
	}
	if err := <-writeResult; err != nil {
		t.Fatalf("concurrent legacy write: %v", err)
	}

	const occurrenceID = `["task-race","2026-07-25"]`
	assertSQLiteLedger(t, installerDB, workspaceID, ReplayEntityOccurrence, occurrenceID, 1, false)
	var sourceRows, outboxRows int
	if err := installerDB.QueryRow(`SELECT COUNT(*) FROM task_occurrences
		WHERE workspace_id=? AND task_id='task-race' AND occurrence_date='2026-07-25'`, workspaceID).Scan(&sourceRows); err != nil {
		t.Fatalf("count concurrent source row: %v", err)
	}
	if err := installerDB.QueryRow(`SELECT COUNT(*) FROM task_domain_legacy_outbox
		WHERE workspace_id=? AND entity_kind='occurrence' AND entity_id=?`, workspaceID, occurrenceID).Scan(&outboxRows); err != nil {
		t.Fatalf("count concurrent outbox row: %v", err)
	}
	if sourceRows != 1 {
		t.Fatalf("source rows = %d, want 1", sourceRows)
	}
	// If the writer won the race, the baseline owns version one and no event
	// is needed. If installation won, the new trigger owns version one and
	// emits exactly one event. Either result proves there is no unversioned
	// committed row in the installation window.
	if outboxRows < 0 || outboxRows > 1 {
		t.Fatalf("outbox rows = %d, want 0 or 1", outboxRows)
	}
}

func TestInstallLegacyOutboxTriggersSQLiteRollsBackEarlierDDLWhenLaterDDLFails(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)

	// A view exposes the complete canonical inventory, so planning succeeds.
	// SQLite cannot attach an AFTER row trigger to it. task_projects appears
	// first in the manifest, proving its already-created triggers are rolled
	// back when installation reaches this later source.
	mustExecSQLiteOutbox(t, db, `DROP TABLE tasks`)
	mustExecSQLiteOutbox(t, db, `CREATE VIEW tasks AS SELECT
		NULL AS completed_at, NULL AS content, 0 AS done, NULL AS due_at, NULL AS execution_type,
		NULL AS horizon, '' AS id, NULL AS note_id, NULL AS planned_date, 0 AS priority,
		NULL AS project_id, NULL AS roadmap_node_id, NULL AS scope, 0 AS sort_order, NULL AS status,
		'' AS title, CURRENT_TIMESTAMP AS updated_at, '' AS workspace_id`)

	err := InstallLegacyOutboxTriggers(context.Background(), db, DialectSQLite, TaskDomainSourceLegacyWorkspace)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "install") {
		t.Fatalf("error = %v, want installation failure", err)
	}
	if got := countInstalledLegacyOutboxTriggers(t, db); got != 0 {
		t.Fatalf("installed trigger count after rolled-back DDL = %d, want 0", got)
	}
}

func TestInstallLegacyOutboxTriggersPostgresAcceptsHistoricalEventTimestampColumns(t *testing.T) {
	db := openPostgresOutboxIntegrationDB(t)
	createPostgresOutboxFixtureSchema(t, db)
	mustExecPostgresOutbox(t, db, `ALTER TABLE events RENAME COLUMN start_time TO start_at`)
	mustExecPostgresOutbox(t, db, `ALTER TABLE events RENAME COLUMN end_time TO end_at`)

	for attempt := 1; attempt <= 2; attempt++ {
		if err := InstallLegacyOutboxTriggers(context.Background(), db, DialectPostgres, TaskDomainSourceLegacyWorkspace); err != nil {
			t.Fatalf("InstallLegacyOutboxTriggers PostgreSQL attempt %d: %v", attempt, err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*)
			FROM pg_trigger trigger
			JOIN pg_class source ON source.oid = trigger.tgrelid
			JOIN pg_namespace namespace ON namespace.oid = source.relnamespace
			WHERE NOT trigger.tgisinternal
			  AND namespace.nspname = current_schema()
			  AND trigger.tgname LIKE 'task_domain_legacy_outbox_%'`).Scan(&count); err != nil {
			t.Fatalf("count PostgreSQL legacy outbox triggers: %v", err)
		}
		if count != 15 {
			t.Fatalf("PostgreSQL trigger count after attempt %d = %d, want 15", attempt, count)
		}
	}
}

func countInstalledLegacyOutboxTriggers(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='trigger' AND name LIKE 'task_domain_legacy_outbox_%'`).Scan(&count); err != nil {
		t.Fatalf("count installed legacy outbox triggers: %v", err)
	}
	return count
}
