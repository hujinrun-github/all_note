package taskmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lib/pq"
)

func TestPostgresLegacyOutboxTriggersPreserveImagesVersionsAndFence(t *testing.T) {
	db := openPostgresOutboxIntegrationDB(t)
	createPostgresOutboxFixtureSchema(t, db)

	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}
	rendered, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxSQL: %v", err)
	}
	// Installation is deliberately transactional and repeatable. The second
	// pass exercises CREATE OR REPLACE plus DROP TRIGGER IF EXISTS against the
	// exact same schema and rendered DDL.
	installPostgresOutboxTriggers(t, db, rendered)
	installPostgresOutboxTriggers(t, db, rendered)

	const workspaceID = "workspace-outbox-contract"
	mustExecPostgresOutbox(t, db, `INSERT INTO workspace_task_domain_state
		(workspace_id, accept_legacy_writes) VALUES ($1, TRUE)`, workspaceID)

	mustExecPostgresOutbox(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES ($1, 'project-image', 'Project before', 'short')`, workspaceID)
	mustExecPostgresOutbox(t, db, `UPDATE task_projects SET name='Project after'
		WHERE workspace_id=$1 AND id='project-image'`, workspaceID)
	mustExecPostgresOutbox(t, db, `DELETE FROM task_projects
		WHERE workspace_id=$1 AND id='project-image'`, workspaceID)

	insertOccurrence := `INSERT INTO task_occurrences
		(workspace_id, task_id, occurrence_date, status, note, completed_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, NULL, now())`
	mustExecPostgresOutbox(t, db, insertOccurrence, workspaceID, "task/shared", "2026-07-22", "Occurrence before")
	mustExecPostgresOutbox(t, db, `UPDATE task_occurrences SET note='Occurrence after', updated_at=now()
		WHERE workspace_id=$1 AND task_id='task/shared' AND occurrence_date='2026-07-22'`, workspaceID)
	mustExecPostgresOutbox(t, db, `DELETE FROM task_occurrences
		WHERE workspace_id=$1 AND task_id='task/shared' AND occurrence_date='2026-07-22'`, workspaceID)
	mustExecPostgresOutbox(t, db, insertOccurrence, workspaceID, "task/shared", "2026-07-23", "Occurrence retained")

	events := readPostgresOutboxEvents(t, db, workspaceID)
	if len(events) != 7 {
		t.Fatalf("outbox event count = %d, want 7: %#v", len(events), events)
	}
	for index := 1; index < len(events); index++ {
		if events[index].Sequence <= events[index-1].Sequence {
			t.Fatalf("outbox sequences are not strictly increasing at %d: %d then %d", index, events[index-1].Sequence, events[index].Sequence)
		}
	}

	assertPostgresOutboxImage(t, events[0], ReplayEntityProject, "project-image", "upsert", 1, "row", "name", "Project before")
	assertPostgresOutboxImage(t, events[1], ReplayEntityProject, "project-image", "upsert", 2, "row", "name", "Project after")
	assertPostgresOutboxImage(t, events[2], ReplayEntityProject, "project-image", "delete", 3, "tombstone", "name", "Project after")

	firstOccurrenceID := assertPostgresOccurrenceOutboxID(t, events[3], "task/shared", "2026-07-22")
	assertPostgresOutboxImage(t, events[3], ReplayEntityOccurrence, firstOccurrenceID, "upsert", 1, "row", "note", "Occurrence before")
	assertPostgresOutboxImage(t, events[4], ReplayEntityOccurrence, firstOccurrenceID, "upsert", 2, "row", "note", "Occurrence after")
	assertPostgresOutboxImage(t, events[5], ReplayEntityOccurrence, firstOccurrenceID, "delete", 3, "tombstone", "note", "Occurrence after")
	secondOccurrenceID := assertPostgresOccurrenceOutboxID(t, events[6], "task/shared", "2026-07-23")
	if firstOccurrenceID == secondOccurrenceID {
		t.Fatalf("occurrence composite identities collided: %q", firstOccurrenceID)
	}
	assertPostgresOutboxImage(t, events[6], ReplayEntityOccurrence, secondOccurrenceID, "upsert", 1, "row", "note", "Occurrence retained")

	assertPostgresLedger(t, db, workspaceID, ReplayEntityProject, "project-image", 3, true)
	assertPostgresLedger(t, db, workspaceID, ReplayEntityOccurrence, firstOccurrenceID, 3, true)
	assertPostgresLedger(t, db, workspaceID, ReplayEntityOccurrence, secondOccurrenceID, 1, false)

	mustExecPostgresOutbox(t, db, `UPDATE workspace_task_domain_state
		SET accept_legacy_writes=FALSE WHERE workspace_id=$1`, workspaceID)
	before := readPostgresOutboxFenceSnapshot(t, db, workspaceID, secondOccurrenceID)

	assertPostgresLegacyWriteFenced(t, db, `INSERT INTO task_projects
		(workspace_id, id, name, type) VALUES ($1, 'fenced-project', 'must roll back', 'short')`, workspaceID)
	assertPostgresLegacyWriteFenced(t, db, `UPDATE task_occurrences SET note='must roll back'
		WHERE workspace_id=$1 AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID)
	assertPostgresLegacyWriteFenced(t, db, `DELETE FROM task_occurrences
		WHERE workspace_id=$1 AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID)

	after := readPostgresOutboxFenceSnapshot(t, db, workspaceID, secondOccurrenceID)
	if after != before {
		t.Fatalf("fenced DML changed source/ledger/outbox\nbefore: %+v\nafter:  %+v", before, after)
	}
}

func TestPostgresRoadmapFreezeSerializesStateTransitionWithInFlightWrite(t *testing.T) {
	db := openPostgresOutboxIntegrationDB(t)
	createPostgresOutboxFixtureSchema(t, db)

	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}
	rendered, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxSQL: %v", err)
	}
	installPostgresOutboxTriggers(t, db, rendered)

	const workspaceID = "workspace-roadmap-freeze-race"
	mustExecPostgresOutbox(t, db, `INSERT INTO workspace_task_domain_state
		(workspace_id, accept_legacy_writes, migration_state) VALUES ($1, TRUE, 'idle')`, workspaceID)
	mustExecPostgresOutbox(t, db, `INSERT INTO learning_roadmaps
		(workspace_id, id) VALUES ($1, 'roadmap-race')`, workspaceID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	roadmapConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("open roadmap connection: %v", err)
	}
	defer roadmapConn.Close()
	stateConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("open state connection: %v", err)
	}
	defer stateConn.Close()

	roadmapTx, err := roadmapConn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin roadmap transaction: %v", err)
	}
	defer roadmapTx.Rollback()
	if _, err := roadmapTx.ExecContext(ctx, `UPDATE learning_roadmaps SET id=id
		WHERE workspace_id=$1 AND id='roadmap-race'`, workspaceID); err != nil {
		t.Fatalf("execute in-flight roadmap write: %v", err)
	}

	var stateBackendPID int
	if err := stateConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&stateBackendPID); err != nil {
		t.Fatalf("read state connection backend PID: %v", err)
	}
	stateUpdateDone := make(chan error, 1)
	go func() {
		_, updateErr := stateConn.ExecContext(ctx, `UPDATE workspace_task_domain_state
			SET migration_state='backfilling' WHERE workspace_id=$1`, workspaceID)
		stateUpdateDone <- updateErr
	}()

	waitDeadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock', FALSE)
			FROM pg_stat_activity WHERE pid=$1`, stateBackendPID).Scan(&waiting)
		if err != nil {
			t.Fatalf("inspect state transition lock wait: %v", err)
		}
		if waiting {
			break
		}
		select {
		case updateErr := <-stateUpdateDone:
			t.Fatalf("state transition completed before roadmap transaction drained: %v", updateErr)
		default:
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("state transition did not wait on roadmap FOR SHARE lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := roadmapTx.Commit(); err != nil {
		t.Fatalf("commit in-flight roadmap write: %v", err)
	}
	select {
	case updateErr := <-stateUpdateDone:
		if updateErr != nil {
			t.Fatalf("complete state transition after drain: %v", updateErr)
		}
	case <-ctx.Done():
		t.Fatalf("state transition remained blocked after roadmap commit: %v", ctx.Err())
	}

	_, err = db.ExecContext(ctx, `UPDATE learning_roadmaps SET id=id
		WHERE workspace_id=$1 AND id='roadmap-race'`, workspaceID)
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55000" || pgError.Message != "legacy_roadmap_frozen" {
		t.Fatalf("roadmap write after freeze error = %T %v, want PostgreSQL 55000 legacy_roadmap_frozen", err, err)
	}
}

type postgresOutboxEvent struct {
	Sequence       int64
	EntityKind     ReplayEntityKind
	EntityID       string
	Operation      string
	LogicalVersion int64
	RowImage       []byte
	TombstoneImage []byte
}

type postgresOutboxFenceSnapshot struct {
	ProjectRows              int
	OccurrenceRows           int
	LedgerRows               int
	OutboxRows               int
	RetainedOccurrenceNote   string
	RetainedLogicalVersion   int64
	RetainedOccurrenceDelete bool
}

func openPostgresOutboxIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL"))
	if baseURL == "" {
		t.Skip("FLOWSPACE_TEST_DATABASE_URL is required for PostgreSQL outbox integration tests")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Hostname() == "" {
		t.Fatalf("FLOWSPACE_TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	// Project memory explicitly retires this host. Reject it before sql.Open so
	// a stale local environment can never contact the old PostgreSQL instance.
	if parsed.Hostname() == "192.168.1.20" {
		t.Fatal("FLOWSPACE_TEST_DATABASE_URL points at the retired PostgreSQL host")
	}

	admin, err := sql.Open("pgx", baseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration connection: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("ping PostgreSQL integration connection: %v", err)
	}

	schema := fmt.Sprintf("fs_test_taskmigration_outbox_%d", time.Now().UnixNano())
	quotedSchema := pq.QuoteIdentifier(schema)
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+quotedSchema); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated PostgreSQL schema: %v", err)
	}

	schemaURL := *parsed
	query := schemaURL.Query()
	query.Set("options", "-c search_path="+schema+",public")
	schemaURL.RawQuery = query.Encode()
	db, err := sql.Open("pgx", schemaURL.String())
	if err != nil {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_ = admin.Close()
		t.Fatalf("open isolated PostgreSQL schema: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`)
		_ = admin.Close()
		t.Fatalf("ping isolated PostgreSQL schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA IF EXISTS `+quotedSchema+` CASCADE`); err != nil {
			t.Errorf("drop isolated PostgreSQL schema %q: %v", schema, err)
		}
		_ = admin.Close()
	})
	return db
}

func createPostgresOutboxFixtureSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	const ddl = `
CREATE TABLE workspace_task_domain_state (
  workspace_id TEXT PRIMARY KEY,
  accept_legacy_writes BOOLEAN NOT NULL,
  migration_state TEXT NOT NULL DEFAULT 'idle'
);
CREATE TABLE legacy_task_domain_entity_versions (
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  logical_version BIGINT NOT NULL,
  deleted BOOLEAN NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (workspace_id, entity_kind, entity_id)
);
CREATE TABLE task_domain_legacy_outbox (
  sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  entity_kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  source_logical_version BIGINT NOT NULL,
  row_image JSONB,
  tombstone_image JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE task_projects (
  id TEXT NOT NULL,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE tasks (
  completed_at TIMESTAMPTZ,
  content TEXT,
  done BOOLEAN NOT NULL,
  due_at TIMESTAMPTZ,
  execution_type TEXT,
  horizon TEXT,
  id TEXT NOT NULL,
  note_id TEXT,
  planned_date DATE,
  priority INTEGER NOT NULL,
  project_id TEXT,
  roadmap_node_id TEXT,
  scope TEXT,
  sort_order INTEGER NOT NULL,
  status TEXT,
  title TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE task_recurrence_rules (
  enabled BOOLEAN NOT NULL,
  end_date DATE,
  frequency TEXT NOT NULL,
  "interval" INTEGER NOT NULL,
  month_days INTEGER[],
  start_date DATE NOT NULL,
  task_id TEXT NOT NULL,
  timezone TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  weekdays INTEGER[],
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id)
);
CREATE TABLE task_occurrences (
  completed_at TIMESTAMPTZ,
  note TEXT,
  occurrence_date DATE NOT NULL,
  status TEXT NOT NULL,
  task_id TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, task_id, occurrence_date)
);
CREATE TABLE events (
  end_time TIMESTAMPTZ NOT NULL,
  id TEXT NOT NULL,
  is_all_day BOOLEAN NOT NULL,
  kind TEXT,
  location TEXT,
  note_id TEXT,
  notes TEXT,
  project_id TEXT,
  start_time TIMESTAMPTZ NOT NULL,
  title TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE learning_roadmaps (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE roadmap_nodes (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);
CREATE TABLE roadmap_edges (
  id TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);`
	mustExecPostgresOutbox(t, db, ddl)
}

func installPostgresOutboxTriggers(t *testing.T, db *sql.DB, ddl string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL outbox trigger installation: %v", err)
	}
	if _, err := tx.Exec(ddl); err != nil {
		_ = tx.Rollback()
		t.Fatalf("execute PostgreSQL outbox trigger installation: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit PostgreSQL outbox trigger installation: %v", err)
	}
}

func readPostgresOutboxEvents(t *testing.T, db *sql.DB, workspaceID string) []postgresOutboxEvent {
	t.Helper()
	rows, err := db.Query(`SELECT sequence, entity_kind, entity_id, operation,
		source_logical_version, row_image, tombstone_image
		FROM task_domain_legacy_outbox WHERE workspace_id=$1 ORDER BY sequence`, workspaceID)
	if err != nil {
		t.Fatalf("query PostgreSQL outbox: %v", err)
	}
	defer rows.Close()
	var result []postgresOutboxEvent
	for rows.Next() {
		var event postgresOutboxEvent
		if err := rows.Scan(&event.Sequence, &event.EntityKind, &event.EntityID, &event.Operation,
			&event.LogicalVersion, &event.RowImage, &event.TombstoneImage); err != nil {
			t.Fatalf("scan PostgreSQL outbox: %v", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate PostgreSQL outbox: %v", err)
	}
	return result
}

func assertPostgresOutboxImage(t *testing.T, event postgresOutboxEvent, kind ReplayEntityKind, entityID, operation string, version int64, imageSide, field string, want any) {
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

func assertPostgresOccurrenceOutboxID(t *testing.T, event postgresOutboxEvent, taskID, date string) string {
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

func assertPostgresLedger(t *testing.T, db *sql.DB, workspaceID string, kind ReplayEntityKind, entityID string, version int64, deleted bool) {
	t.Helper()
	var gotVersion int64
	var gotDeleted bool
	if err := db.QueryRow(`SELECT logical_version, deleted
		FROM legacy_task_domain_entity_versions
		WHERE workspace_id=$1 AND entity_kind=$2 AND entity_id=$3`, workspaceID, kind, entityID).Scan(&gotVersion, &gotDeleted); err != nil {
		t.Fatalf("read PostgreSQL logical-version ledger: %v", err)
	}
	if gotVersion != version || gotDeleted != deleted {
		t.Fatalf("ledger %s/%q = version %d deleted %v, want %d/%v", kind, entityID, gotVersion, gotDeleted, version, deleted)
	}
}

func assertPostgresLegacyWriteFenced(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	_, err := db.Exec(statement, args...)
	if err == nil {
		t.Fatal("legacy DML succeeded while accept_legacy_writes=false")
	}
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55000" || pgError.Message != "legacy_task_domain_fenced" {
		t.Fatalf("fenced DML error = %T %v, want PostgreSQL 55000 legacy_task_domain_fenced", err, err)
	}
}

func readPostgresOutboxFenceSnapshot(t *testing.T, db *sql.DB, workspaceID, retainedOccurrenceID string) postgresOutboxFenceSnapshot {
	t.Helper()
	var snapshot postgresOutboxFenceSnapshot
	queries := []struct {
		query string
		dest  *int
	}{
		{`SELECT COUNT(*) FROM task_projects WHERE workspace_id=$1`, &snapshot.ProjectRows},
		{`SELECT COUNT(*) FROM task_occurrences WHERE workspace_id=$1`, &snapshot.OccurrenceRows},
		{`SELECT COUNT(*) FROM legacy_task_domain_entity_versions WHERE workspace_id=$1`, &snapshot.LedgerRows},
		{`SELECT COUNT(*) FROM task_domain_legacy_outbox WHERE workspace_id=$1`, &snapshot.OutboxRows},
	}
	for _, query := range queries {
		if err := db.QueryRow(query.query, workspaceID).Scan(query.dest); err != nil {
			t.Fatalf("read fenced state count: %v", err)
		}
	}
	if err := db.QueryRow(`SELECT note FROM task_occurrences
		WHERE workspace_id=$1 AND task_id='task/shared' AND occurrence_date='2026-07-23'`, workspaceID).
		Scan(&snapshot.RetainedOccurrenceNote); err != nil {
		t.Fatalf("read retained occurrence: %v", err)
	}
	if err := db.QueryRow(`SELECT logical_version, deleted
		FROM legacy_task_domain_entity_versions
		WHERE workspace_id=$1 AND entity_kind='occurrence' AND entity_id=$2`, workspaceID, retainedOccurrenceID).
		Scan(&snapshot.RetainedLogicalVersion, &snapshot.RetainedOccurrenceDelete); err != nil {
		t.Fatalf("read retained occurrence ledger: %v", err)
	}
	return snapshot
}

func mustExecPostgresOutbox(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("execute PostgreSQL outbox fixture statement: %v", err)
	}
}
