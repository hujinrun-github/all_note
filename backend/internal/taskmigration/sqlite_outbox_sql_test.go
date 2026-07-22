package taskmigration

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRenderSQLiteLegacyOutboxSQLFreshV2IsEmpty(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(SchemaInventory{Mode: TaskDomainSourceFreshV2})
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	got, err := RenderSQLiteLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderSQLiteLegacyOutboxSQL: %v", err)
	}
	if got != "" {
		t.Fatalf("fresh-v2 SQL = %q, want empty", got)
	}
}

func TestRenderSQLiteLegacyOutboxBaselineSQLUsesCanonicalIdentityAndPreservesExistingVersions(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	got, err := RenderSQLiteLegacyOutboxBaselineSQL(plan)
	if err != nil {
		t.Fatalf("RenderSQLiteLegacyOutboxBaselineSQL: %v", err)
	}
	assertSQLCount(t, got, "INSERT INTO legacy_task_domain_entity_versions", len(LegacyOutboxManifest())+3)
	assertSQLCount(t, got, "DO NOTHING", len(LegacyOutboxManifest())+3)
	if strings.Contains(got, "DO UPDATE") {
		t.Fatalf("baseline SQL can reset existing logical versions:\n%s", got)
	}
	wantOccurrenceIdentity := `json_array(CAST(source."task_id" AS TEXT), CAST(source."occurrence_date" AS TEXT))`
	if !strings.Contains(got, wantOccurrenceIdentity) {
		t.Fatalf("baseline occurrence identity does not match trigger identity: %s", got)
	}
}

func TestRenderSQLiteLegacyOutboxSQLCoversEverySourceDML(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	sqlText, err := RenderSQLiteLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderSQLiteLegacyOutboxSQL: %v", err)
	}
	wantTriggers := len(LegacyOutboxManifest())*3 + len(LegacyRoadmapFreezeTriggerManifest())
	if got := strings.Count(sqlText, "CREATE TRIGGER "); got != wantTriggers {
		t.Fatalf("CREATE TRIGGER count = %d, want %d\n%s", got, wantTriggers, sqlText)
	}
	if got := strings.Count(sqlText, "DROP TRIGGER IF EXISTS "); got != wantTriggers {
		t.Fatalf("DROP TRIGGER count = %d, want %d", got, wantTriggers)
	}
	for _, manifest := range LegacyOutboxManifest() {
		for _, operation := range []string{"insert", "update", "delete"} {
			name := `"task_domain_legacy_outbox_` + manifest.Table + `_` + operation + `"`
			if !strings.Contains(sqlText, "CREATE TRIGGER "+name) {
				t.Errorf("missing trigger %s", name)
			}
		}
	}
}

func TestRenderSQLiteLegacyOutboxSQLPreservesFenceLedgerAndImages(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	sqlText, err := RenderSQLiteLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderSQLiteLegacyOutboxSQL: %v", err)
	}

	standardTriggers := len(LegacyOutboxManifest()) * 3
	allTriggers := standardTriggers + len(LegacyRoadmapFreezeTriggerManifest())
	assertSQLCount(t, sqlText, "FROM workspace_task_domain_state", allTriggers)
	assertSQLCount(t, sqlText, "RAISE(ABORT, 'legacy_task_domain_fenced')", 15)
	assertSQLCount(t, sqlText, "INSERT INTO legacy_task_domain_entity_versions", allTriggers)
	assertSQLCount(t, sqlText, "logical_version = legacy_task_domain_entity_versions.logical_version + 1", 15)
	assertSQLCount(t, sqlText, "INSERT INTO task_domain_legacy_outbox", 15)
	assertSQLCount(t, sqlText, "json_object(", 15)
	assertSQLCount(t, sqlText, "'upsert'", 10)
	assertSQLCount(t, sqlText, "'delete'", 5)

	for _, operation := range []string{"insert", "update"} {
		body := sqliteTriggerBody(t, sqlText, "task_occurrences", operation)
		wantIdentity := `json_array(CAST(NEW."task_id" AS TEXT), CAST(NEW."occurrence_date" AS TEXT))`
		if !strings.Contains(body, wantIdentity) {
			t.Errorf("%s occurrence identity is not canonical JSON: %s", operation, body)
		}
		if !strings.Contains(body, `json_object('completed_at', NEW."completed_at"`) {
			t.Errorf("%s occurrence after-image is not built from NEW: %s", operation, body)
		}
	}
	deleteBody := sqliteTriggerBody(t, sqlText, "task_occurrences", "delete")
	wantDeleteIdentity := `json_array(CAST(OLD."task_id" AS TEXT), CAST(OLD."occurrence_date" AS TEXT))`
	if !strings.Contains(deleteBody, wantDeleteIdentity) {
		t.Errorf("delete occurrence identity is not canonical JSON: %s", deleteBody)
	}
	if !strings.Contains(deleteBody, `json_object('completed_at', OLD."completed_at"`) {
		t.Errorf("occurrence tombstone is not built from OLD: %s", deleteBody)
	}
}

func TestRenderSQLiteLegacyOutboxSQLIsDeterministic(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	first, err := RenderSQLiteLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	for iteration := 0; iteration < 10; iteration++ {
		got, renderErr := RenderSQLiteLegacyOutboxSQL(plan)
		if renderErr != nil {
			t.Fatalf("render %d: %v", iteration, renderErr)
		}
		if got != first {
			t.Fatalf("render %d changed output", iteration)
		}
	}
}

func TestRenderSQLiteLegacyOutboxSQLRejectsTamperedPlans(t *testing.T) {
	validPlan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(TriggerPlan) TriggerPlan
	}{
		{
			name: "injected table",
			mutate: func(plan TriggerPlan) TriggerPlan {
				plan.Upserts[0].Table = `task_projects"; DROP TABLE tasks; --`
				return plan
			},
		},
		{
			name: "injected image column",
			mutate: func(plan TriggerPlan) TriggerPlan {
				plan.Upserts[0].RequiredColumns[0] = `id', NEW.id); DROP TABLE tasks; --`
				return plan
			},
		},
		{
			name: "missing trigger",
			mutate: func(plan TriggerPlan) TriggerPlan {
				plan.Deletes = plan.Deletes[:len(plan.Deletes)-1]
				return plan
			},
		},
		{
			name: "wrong operation",
			mutate: func(plan TriggerPlan) TriggerPlan {
				plan.Upserts[0].Operation = TriggerDelete
				return plan
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, cloneErr := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
			if cloneErr != nil {
				t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", cloneErr)
			}
			got, renderErr := RenderSQLiteLegacyOutboxSQL(test.mutate(plan))
			if !errors.Is(renderErr, ErrInvalidSQLiteLegacyOutboxPlan) {
				t.Fatalf("error = %v, want ErrInvalidSQLiteLegacyOutboxPlan", renderErr)
			}
			if got != "" {
				t.Fatalf("rejected plan rendered SQL: %q", got)
			}
		})
	}

	fresh := TriggerPlan{Mode: TaskDomainSourceFreshV2, Upserts: validPlan.Upserts[:1]}
	if got, renderErr := RenderSQLiteLegacyOutboxSQL(fresh); !errors.Is(renderErr, ErrInvalidSQLiteLegacyOutboxPlan) || got != "" {
		t.Fatalf("contaminated fresh plan = (%q, %v)", got, renderErr)
	}
	if got, renderErr := RenderSQLiteLegacyOutboxSQL(TriggerPlan{Mode: "unknown"}); !errors.Is(renderErr, ErrInvalidSQLiteLegacyOutboxPlan) || got != "" {
		t.Fatalf("unknown-mode plan = (%q, %v)", got, renderErr)
	}
}

func TestSQLiteLegacyOutboxTriggersPreserveImagesVersionsIdentityAndFence(t *testing.T) {
	db := openSQLiteOutboxTestDB(t)
	createSQLiteOutboxFixtureSchema(t, db)

	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}
	rendered, err := RenderSQLiteLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderSQLiteLegacyOutboxSQL: %v", err)
	}
	installSQLiteOutboxTriggers(t, db, rendered)
	installSQLiteOutboxTriggers(t, db, rendered)

	const workspaceID = "workspace-outbox-contract"
	mustExecSQLiteOutbox(t, db, `INSERT INTO workspace_task_domain_state
		(workspace_id, accept_legacy_writes) VALUES (?, 1)`, workspaceID)

	mustExecSQLiteOutbox(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES (?, 'project-image', 'Project before', 'short')`, workspaceID)
	mustExecSQLiteOutbox(t, db, `UPDATE task_projects SET name='Project after'
		WHERE workspace_id=? AND id='project-image'`, workspaceID)
	mustExecSQLiteOutbox(t, db, `DELETE FROM task_projects
		WHERE workspace_id=? AND id='project-image'`, workspaceID)

	insertOccurrence := `INSERT INTO task_occurrences
		(workspace_id, task_id, occurrence_date, status, note, completed_at, updated_at)
		VALUES (?, ?, ?, 'pending', ?, NULL, CURRENT_TIMESTAMP)`
	mustExecSQLiteOutbox(t, db, insertOccurrence, workspaceID, "task/shared", "2026-07-22", "Occurrence before")
	mustExecSQLiteOutbox(t, db, `UPDATE task_occurrences SET note='Occurrence after', updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND task_id='task/shared' AND occurrence_date='2026-07-22'`, workspaceID)
	mustExecSQLiteOutbox(t, db, `DELETE FROM task_occurrences
		WHERE workspace_id=? AND task_id='task/shared' AND occurrence_date='2026-07-22'`, workspaceID)
	mustExecSQLiteOutbox(t, db, insertOccurrence, workspaceID, "task/shared", "2026-07-23", "Occurrence retained")

	events := readSQLiteOutboxEvents(t, db, workspaceID)
	if len(events) != 7 {
		t.Fatalf("outbox event count = %d, want 7: %#v", len(events), events)
	}
	assertSQLiteOutboxImage(t, events[0], ReplayEntityProject, "project-image", "upsert", 1, "row", "name", "Project before")
	assertSQLiteOutboxImage(t, events[1], ReplayEntityProject, "project-image", "upsert", 2, "row", "name", "Project after")
	assertSQLiteOutboxImage(t, events[2], ReplayEntityProject, "project-image", "delete", 3, "tombstone", "name", "Project after")

	firstOccurrenceID := assertSQLiteOccurrenceOutboxID(t, events[3], "task/shared", "2026-07-22")
	assertSQLiteOutboxImage(t, events[3], ReplayEntityOccurrence, firstOccurrenceID, "upsert", 1, "row", "note", "Occurrence before")
	assertSQLiteOutboxImage(t, events[4], ReplayEntityOccurrence, firstOccurrenceID, "upsert", 2, "row", "note", "Occurrence after")
	assertSQLiteOutboxImage(t, events[5], ReplayEntityOccurrence, firstOccurrenceID, "delete", 3, "tombstone", "note", "Occurrence after")
	secondOccurrenceID := assertSQLiteOccurrenceOutboxID(t, events[6], "task/shared", "2026-07-23")
	if firstOccurrenceID == secondOccurrenceID {
		t.Fatalf("occurrence composite identities collided: %q", firstOccurrenceID)
	}
	assertSQLiteOutboxImage(t, events[6], ReplayEntityOccurrence, secondOccurrenceID, "upsert", 1, "row", "note", "Occurrence retained")

	assertSQLiteLedger(t, db, workspaceID, ReplayEntityProject, "project-image", 3, true)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityOccurrence, firstOccurrenceID, 3, true)
	assertSQLiteLedger(t, db, workspaceID, ReplayEntityOccurrence, secondOccurrenceID, 1, false)

	mustExecSQLiteOutbox(t, db, `UPDATE workspace_task_domain_state
		SET accept_legacy_writes=0 WHERE workspace_id=?`, workspaceID)
	before := readSQLiteOutboxFenceSnapshot(t, db, workspaceID, secondOccurrenceID)
	assertSQLiteLegacyWriteFenced(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES (?, 'fenced-project', 'must roll back', 'short')`, workspaceID)
	assertSQLiteLegacyWriteFenced(t, db, `UPDATE task_occurrences SET note='must roll back'
		WHERE workspace_id=? AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID)
	assertSQLiteLegacyWriteFenced(t, db, `DELETE FROM task_occurrences
		WHERE workspace_id=? AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID)
	after := readSQLiteOutboxFenceSnapshot(t, db, workspaceID, secondOccurrenceID)
	if after != before {
		t.Fatalf("fenced DML changed source/ledger/outbox\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func sqliteTriggerBody(t *testing.T, sqlText, table, operation string) string {
	t.Helper()
	name := `"task_domain_legacy_outbox_` + table + `_` + operation + `"`
	start := strings.Index(sqlText, "CREATE TRIGGER "+name)
	if start < 0 {
		t.Fatalf("trigger %s not found", name)
	}
	endRelative := strings.Index(sqlText[start:], "END;")
	if endRelative < 0 {
		t.Fatalf("trigger %s terminator not found", name)
	}
	return sqlText[start : start+endRelative]
}

type sqliteOutboxEvent struct {
	Sequence       int64
	EntityKind     ReplayEntityKind
	EntityID       string
	Operation      string
	LogicalVersion int64
	RowImage       []byte
	TombstoneImage []byte
}

type sqliteOutboxFenceSnapshot struct {
	ProjectRows              int
	OccurrenceRows           int
	LedgerRows               int
	OutboxRows               int
	RetainedOccurrenceNote   string
	RetainedLogicalVersion   int64
	RetainedOccurrenceDelete bool
}

func openSQLiteOutboxTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy-outbox.db"))
	if err != nil {
		t.Fatalf("open SQLite outbox database: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		t.Fatalf("enable SQLite foreign keys: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func createSQLiteOutboxFixtureSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY,
  accept_legacy_writes INTEGER NOT NULL,
  migration_state TEXT NOT NULL DEFAULT 'idle'
);
CREATE TABLE legacy_task_domain_entity_versions (
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  logical_version INTEGER NOT NULL,
  deleted INTEGER NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (workspace_id, entity_kind, entity_id)
);
CREATE TABLE task_domain_legacy_outbox (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  source_logical_version INTEGER NOT NULL,
  row_image TEXT,
  tombstone_image TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE task_projects (
  id TEXT NOT NULL, name TEXT NOT NULL, type TEXT NOT NULL, workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE tasks (
  completed_at TEXT, content TEXT, done INTEGER NOT NULL, due_at TEXT, execution_type TEXT,
  horizon TEXT, id TEXT NOT NULL, note_id TEXT, planned_date TEXT, priority INTEGER NOT NULL,
  project_id TEXT, roadmap_node_id TEXT, scope TEXT, sort_order INTEGER NOT NULL, status TEXT,
  title TEXT NOT NULL, updated_at TEXT NOT NULL, workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE task_recurrence_rules (
  enabled INTEGER NOT NULL, end_date TEXT, frequency TEXT NOT NULL, "interval" INTEGER NOT NULL,
  month_days TEXT, start_date TEXT NOT NULL, task_id TEXT NOT NULL, timezone TEXT NOT NULL,
  updated_at TEXT NOT NULL, weekdays TEXT, workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id)
);
CREATE TABLE task_occurrences (
  completed_at TEXT, note TEXT, occurrence_date TEXT NOT NULL, status TEXT NOT NULL,
  task_id TEXT NOT NULL, updated_at TEXT NOT NULL, workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id, occurrence_date)
);
CREATE TABLE events (
  end_time TEXT NOT NULL, id TEXT NOT NULL, is_all_day INTEGER NOT NULL, kind TEXT,
  location TEXT, note_id TEXT, notes TEXT, project_id TEXT, start_time TEXT NOT NULL,
  title TEXT NOT NULL, updated_at TEXT NOT NULL, workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE learning_roadmaps (id TEXT NOT NULL,workspace_id TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE roadmap_nodes (id TEXT NOT NULL,workspace_id TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE roadmap_edges (id TEXT NOT NULL,workspace_id TEXT NOT NULL,PRIMARY KEY(workspace_id,id)
);`
	mustExecSQLiteOutbox(t, db, ddl)
}

func installSQLiteOutboxTriggers(t *testing.T, db *sql.DB, ddl string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin SQLite trigger installation: %v", err)
	}
	if _, err := tx.Exec(ddl); err != nil {
		_ = tx.Rollback()
		t.Fatalf("execute SQLite trigger installation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit SQLite trigger installation: %v", err)
	}
}

func readSQLiteOutboxEvents(t *testing.T, db *sql.DB, workspaceID string) []sqliteOutboxEvent {
	t.Helper()
	rows, err := db.Query(`SELECT sequence, entity_kind, entity_id, operation,
		source_logical_version, row_image, tombstone_image
		FROM task_domain_legacy_outbox WHERE workspace_id=? ORDER BY sequence`, workspaceID)
	if err != nil {
		t.Fatalf("query SQLite outbox: %v", err)
	}
	defer rows.Close()
	var result []sqliteOutboxEvent
	for rows.Next() {
		var event sqliteOutboxEvent
		if err := rows.Scan(&event.Sequence, &event.EntityKind, &event.EntityID, &event.Operation,
			&event.LogicalVersion, &event.RowImage, &event.TombstoneImage); err != nil {
			t.Fatalf("scan SQLite outbox: %v", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite outbox: %v", err)
	}
	return result
}

func assertSQLiteOutboxImage(t *testing.T, event sqliteOutboxEvent, kind ReplayEntityKind, entityID, operation string, version int64, imageSide, field string, want any) {
	t.Helper()
	if event.EntityKind != kind || event.EntityID != entityID || event.Operation != operation || event.LogicalVersion != version {
		t.Fatalf("outbox envelope = %+v, want kind=%s id=%q operation=%s version=%d", event, kind, entityID, operation, version)
	}
	var image []byte
	switch imageSide {
	case "row":
		if len(event.RowImage) == 0 || len(event.TombstoneImage) != 0 {
			t.Fatalf("upsert images = row %s tombstone %s", event.RowImage, event.TombstoneImage)
		}
		image = event.RowImage
	case "tombstone":
		if len(event.RowImage) != 0 || len(event.TombstoneImage) == 0 {
			t.Fatalf("delete images = row %s tombstone %s", event.RowImage, event.TombstoneImage)
		}
		image = event.TombstoneImage
	default:
		t.Fatalf("unknown image side %q", imageSide)
	}
	var decoded map[string]any
	if err := json.Unmarshal(image, &decoded); err != nil {
		t.Fatalf("decode %s image %s: %v", imageSide, image, err)
	}
	if got := decoded[field]; got != want {
		t.Fatalf("%s image field %q = %#v, want %#v; image=%s", imageSide, field, got, want, image)
	}
}

func assertSQLiteOccurrenceOutboxID(t *testing.T, event sqliteOutboxEvent, taskID, date string) string {
	t.Helper()
	if event.EntityKind != ReplayEntityOccurrence {
		t.Fatalf("outbox kind = %q, want occurrence", event.EntityKind)
	}
	var identity []string
	if err := json.Unmarshal([]byte(event.EntityID), &identity); err != nil {
		t.Fatalf("occurrence entity ID %q is not canonical JSON: %v", event.EntityID, err)
	}
	if len(identity) != 2 || identity[0] != taskID || identity[1] != date {
		t.Fatalf("occurrence entity ID = %#v, want [%q %q]", identity, taskID, date)
	}
	return event.EntityID
}

func assertSQLiteLedger(t *testing.T, db *sql.DB, workspaceID string, kind ReplayEntityKind, entityID string, version int64, deleted bool) {
	t.Helper()
	var gotVersion int64
	var gotDeleted bool
	if err := db.QueryRow(`SELECT logical_version, deleted
		FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`, workspaceID, kind, entityID).Scan(&gotVersion, &gotDeleted); err != nil {
		t.Fatalf("read SQLite logical-version ledger: %v", err)
	}
	if gotVersion != version || gotDeleted != deleted {
		t.Fatalf("ledger %s/%q = version %d deleted %v, want %d/%v", kind, entityID, gotVersion, gotDeleted, version, deleted)
	}
}

func assertSQLiteLegacyWriteFenced(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	_, err := db.Exec(statement, args...)
	if err == nil {
		t.Fatal("legacy DML succeeded while accept_legacy_writes=false")
	}
	if !strings.Contains(err.Error(), "legacy_task_domain_fenced") {
		t.Fatalf("fenced DML error = %T %v, want legacy_task_domain_fenced", err, err)
	}
}

func readSQLiteOutboxFenceSnapshot(t *testing.T, db *sql.DB, workspaceID, retainedOccurrenceID string) sqliteOutboxFenceSnapshot {
	t.Helper()
	var snapshot sqliteOutboxFenceSnapshot
	queries := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM task_projects WHERE workspace_id=?`, &snapshot.ProjectRows},
		{`SELECT COUNT(*) FROM task_occurrences WHERE workspace_id=?`, &snapshot.OccurrenceRows},
		{`SELECT COUNT(*) FROM legacy_task_domain_entity_versions WHERE workspace_id=?`, &snapshot.LedgerRows},
		{`SELECT COUNT(*) FROM task_domain_legacy_outbox WHERE workspace_id=?`, &snapshot.OutboxRows},
	}
	for _, query := range queries {
		if err := db.QueryRow(query.query, workspaceID).Scan(query.dest); err != nil {
			t.Fatalf("read fenced state count: %v", err)
		}
	}
	if err := db.QueryRow(`SELECT note FROM task_occurrences
		WHERE workspace_id=? AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID).
		Scan(&snapshot.RetainedOccurrenceNote); err != nil {
		t.Fatalf("read retained occurrence: %v", err)
	}
	if err := db.QueryRow(`SELECT logical_version, deleted
		FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind='occurrence' AND entity_id=?`, workspaceID, retainedOccurrenceID).
		Scan(&snapshot.RetainedLogicalVersion, &snapshot.RetainedOccurrenceDelete); err != nil {
		t.Fatalf("read retained occurrence ledger: %v", err)
	}
	return snapshot
}

func mustExecSQLiteOutbox(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("execute SQLite outbox fixture statement: %v", err)
	}
}
