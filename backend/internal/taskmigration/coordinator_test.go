package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

var errCoordinatorCrash = errors.New("simulated coordinator crash")

type coordinatorFaultOnce struct {
	point MigrationCoordinatorFaultPoint
	fired bool
}

func (f *coordinatorFaultOnce) Inject(_ context.Context, point MigrationCoordinatorFaultPoint) error {
	if !f.fired && point == f.point {
		f.fired = true
		return errCoordinatorCrash
	}
	return nil
}

func TestMigrationCoordinatorSQLiteRecoversEveryDurableBoundaryAndStopsReadyOnLegacy(t *testing.T) {
	db := openCoordinatorSQLite(t)
	seedCoordinatorLegacyTask(t, db, "Original")

	coordinator := newCoordinatorForTest(t, db, MigrationCoordinatorFaultAfterSnapshot)
	_, err := coordinator.RunToReady(context.Background())
	assertCoordinatorCrash(t, err, MigrationStateCatchingUp, "after_snapshot")
	assertCoordinatorState(t, db, MigrationStateCatchingUp, ModelVersionLegacy, 0, true)

	// This write lands after the consistent snapshot. The installed trigger
	// must make it the next replay page rather than letting snapshot recovery
	// silently skip it.
	if _, err := db.Exec(`UPDATE tasks SET title='Updated after snapshot',updated_at='2026-07-22T01:00:00Z'
		WHERE workspace_id='alpha' AND id='task-1'`); err != nil {
		t.Fatal(err)
	}

	coordinator = newCoordinatorForTest(t, db, MigrationCoordinatorFaultAfterReplay)
	_, err = coordinator.RunToReady(context.Background())
	assertCoordinatorCrash(t, err, MigrationStateCatchingUp, "after_replay")
	state := assertCoordinatorState(t, db, MigrationStateCatchingUp, ModelVersionLegacy, 1, true)
	revisionAfterReplay := state.Revision

	coordinator = newCoordinatorForTest(t, db, MigrationCoordinatorFaultAfterDrain)
	_, err = coordinator.RunToReady(context.Background())
	assertCoordinatorCrash(t, err, MigrationStateDraining, "after_drain")
	state = assertCoordinatorState(t, db, MigrationStateDraining, ModelVersionLegacy, 1, false)
	if state.CutoverRevision == nil || *state.CutoverRevision != 1 || state.Revision != revisionAfterReplay+1 {
		t.Fatalf("drain state=%+v", state)
	}
	drainEpoch := state.WriteEpoch

	coordinator = newCoordinatorForTest(t, db, MigrationCoordinatorFaultBeforeReady)
	_, err = coordinator.RunToReady(context.Background())
	assertCoordinatorCrash(t, err, MigrationStateDraining, "before_ready")
	state = assertCoordinatorState(t, db, MigrationStateDraining, ModelVersionLegacy, 1, false)
	if state.WriteEpoch != drainEpoch {
		t.Fatalf("recovery repeated drain epoch: got=%d want=%d", state.WriteEpoch, drainEpoch)
	}

	coordinator = newCoordinatorForTest(t, db, "")
	ready, err := coordinator.RunToReady(context.Background())
	if err != nil {
		t.Fatalf("RunToReady recovery: %v", err)
	}
	if ready.MigrationState != MigrationStateReady || ready.ModelVersion != ModelVersionLegacy || ready.AcceptLegacyWrites {
		t.Fatalf("ready state=%+v", ready)
	}
	if ready.WriteEpoch != drainEpoch || ready.SourceWatermark != 1 || ready.CutoverRevision == nil || *ready.CutoverRevision != 1 {
		t.Fatalf("ready fences=%+v", ready)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Updated after snapshot" {
		t.Fatalf("v2 title=%q", title)
	}
	var logicalVersion int64
	if err := db.QueryRow(`SELECT source_logical_version FROM task_domain_legacy_id_map
		WHERE workspace_id='alpha' AND entity_kind='task' AND legacy_id='task-1' AND target_kind='task'`).Scan(&logicalVersion); err != nil {
		t.Fatal(err)
	}
	if logicalVersion != 2 {
		t.Fatalf("task mapping logical version=%d, want 2", logicalVersion)
	}

	// RunToReady must never perform the model-version CAS, even on a retry.
	again, err := coordinator.RunToReady(context.Background())
	if err != nil || again.ModelVersion != ModelVersionLegacy || again.MigrationState != MigrationStateReady {
		t.Fatalf("idempotent ready run state=%+v err=%v", again, err)
	}
}

func TestMigrationCoordinatorFailureKeepsActionableDurablePhase(t *testing.T) {
	db := openCoordinatorSQLite(t)
	seedCoordinatorLegacyTask(t, db, "Original")
	coordinator := newCoordinatorForTest(t, db, MigrationCoordinatorFaultAfterSnapshot)
	_, err := coordinator.RunToReady(context.Background())
	if !errors.Is(err, errCoordinatorCrash) {
		t.Fatalf("RunToReady error=%v", err)
	}
	state := assertCoordinatorState(t, db, MigrationStateCatchingUp, ModelVersionLegacy, 0, true)
	if state.LastError != "" {
		t.Fatalf("transient process failure rewrote durable state: %+v", state)
	}
}

func TestMigrationCoordinatorRejectsDifferentMigrationIdentityWithoutMutation(t *testing.T) {
	db := openCoordinatorSQLite(t)
	seedCoordinatorLegacyTask(t, db, "Original")
	first := newCoordinatorForTest(t, db, MigrationCoordinatorFaultAfterSnapshot)
	_, _ = first.RunToReady(context.Background())
	before := assertCoordinatorState(t, db, MigrationStateCatchingUp, ModelVersionLegacy, 0, true)

	conflicting, err := NewMigrationCoordinator(MigrationCoordinatorConfig{
		DB: db, Dialect: DialectSQLite, WorkspaceID: "alpha", MigrationID: "different",
		MigrationTimezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := conflicting.Step(context.Background())
	if !errors.Is(err, ErrInvalidMigrationCoordinator) {
		t.Fatalf("conflicting Step error=%v", err)
	}
	if result.State.Revision != before.Revision {
		t.Fatalf("returned state revision=%d want=%d", result.State.Revision, before.Revision)
	}
	after := assertCoordinatorState(t, db, MigrationStateCatchingUp, ModelVersionLegacy, 0, true)
	if after.Revision != before.Revision || after.MigrationID != before.MigrationID {
		t.Fatalf("conflicting identity mutated state: before=%+v after=%+v", before, after)
	}
}

func TestMigrationCoordinatorCutoverDelegatesOnlyThroughGatedService(t *testing.T) {
	db := openCoordinatorSQLite(t)
	seedCoordinatorLegacyTask(t, db, "Original")
	coordinator := newCoordinatorForTest(t, db, "")
	ready, err := coordinator.RunToReady(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	observation := FinalCutoverObservation{
		OutboxWatermark: ready.SourceWatermark, ActiveLegacyTransactions: 0,
		PreviousFenceEpoch: ready.WriteEpoch - 1, Reconcile: ReconcilePlan{Ready: true},
	}
	closed, err := NewCutoverService(CutoverServiceDependencies{
		StateStore: coordinator.state, Observer: coordinatorCutoverObserver{observation: observation},
		Mobile: coordinatorMobileGate{}, Heartbeats: coordinatorHeartbeatGate{count: 1},
		Application: coordinatorV2Capability(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Cutover(context.Background(), closed); !errors.Is(err, ErrCutoverGateClosed) {
		t.Fatalf("closed cutover error=%v", err)
	}
	assertCoordinatorState(t, db, MigrationStateReady, ModelVersionLegacy, ready.SourceWatermark, false)

	open, err := NewCutoverService(CutoverServiceDependencies{
		StateStore: coordinator.state, Observer: coordinatorCutoverObserver{observation: observation},
		Mobile: coordinatorMobileGate{}, Heartbeats: coordinatorHeartbeatGate{},
		Application: coordinatorV2Capability(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Cutover(context.Background(), open)
	if err != nil {
		t.Fatalf("Cutover: %v", err)
	}
	if !result.Applied || result.State.ModelVersion != ModelVersionV2 || result.State.MigrationState != MigrationStateCutover {
		t.Fatalf("cutover result=%+v", result)
	}
}

type coordinatorCutoverObserver struct {
	observation FinalCutoverObservation
}

func (o coordinatorCutoverObserver) ObserveFinalCutover(context.Context, string, string, uint64) (FinalCutoverObservation, error) {
	return o.observation, nil
}

type coordinatorMobileGate struct{}

func (coordinatorMobileGate) Preflight() error { return nil }

type coordinatorHeartbeatGate struct{ count int }

func (g coordinatorHeartbeatGate) CountOldWriterHeartbeats(context.Context, string) (int, error) {
	return g.count, nil
}

type coordinatorV2Capability bool

func (supported coordinatorV2Capability) SupportsTaskDomainV2Schema() bool { return bool(supported) }

func newCoordinatorForTest(t *testing.T, db *sql.DB, fault MigrationCoordinatorFaultPoint) *MigrationCoordinator {
	t.Helper()
	var injector MigrationCoordinatorFaultInjector
	if fault != "" {
		injector = &coordinatorFaultOnce{point: fault}
	}
	coordinator, err := NewMigrationCoordinator(MigrationCoordinatorConfig{
		DB: db, Dialect: DialectSQLite, WorkspaceID: "alpha", MigrationID: "migration-alpha",
		MigrationTimezone: "Asia/Shanghai", ReplayPageSize: 1, MaximumSteps: 100,
		Now:    func() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) },
		Faults: injector,
	})
	if err != nil {
		t.Fatalf("NewMigrationCoordinator: %v", err)
	}
	return coordinator
}

func assertCoordinatorCrash(t *testing.T, err error, phase MigrationState, operation string) {
	t.Helper()
	if !errors.Is(err, errCoordinatorCrash) {
		t.Fatalf("error=%v, want simulated crash", err)
	}
	var execution *MigrationCoordinatorExecutionError
	if !errors.As(err, &execution) || execution.Phase != phase || execution.Operation != operation {
		t.Fatalf("execution error=%+v, want phase=%s operation=%s", execution, phase, operation)
	}
}

func assertCoordinatorState(t *testing.T, db *sql.DB, migrationState MigrationState, model ModelVersion, watermark uint64, acceptLegacy bool) WorkspaceTaskDomainState {
	t.Helper()
	store, err := NewStateStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if state.MigrationState != migrationState || state.ModelVersion != model || state.SourceWatermark != watermark || state.AcceptLegacyWrites != acceptLegacy {
		t.Fatalf("state=%+v, want state=%s model=%s watermark=%d accept=%t", state, migrationState, model, watermark, acceptLegacy)
	}
	return state
}

func openCoordinatorSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "coordinator.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	legacyDDL := `
CREATE TABLE tenant_workspaces(workspace_id TEXT PRIMARY KEY,epoch INTEGER NOT NULL DEFAULT 1,state TEXT NOT NULL DEFAULT 'active',migration_id TEXT,created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE notes(workspace_id TEXT NOT NULL,id TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE task_projects(id TEXT NOT NULL,workspace_id TEXT NOT NULL,name TEXT NOT NULL,type TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,PRIMARY KEY(workspace_id,id));
CREATE TABLE learning_roadmaps(id TEXT NOT NULL,workspace_id TEXT NOT NULL,project_id TEXT NOT NULL,title TEXT NOT NULL DEFAULT '',goal TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'draft',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE roadmap_nodes(id TEXT NOT NULL,workspace_id TEXT NOT NULL,roadmap_id TEXT NOT NULL,parent_id TEXT,type TEXT NOT NULL DEFAULT 'task',title TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',path_type TEXT NOT NULL DEFAULT 'required',status TEXT NOT NULL DEFAULT 'todo',deliverable TEXT NOT NULL DEFAULT '',acceptance_criteria TEXT NOT NULL DEFAULT '',position TEXT NOT NULL DEFAULT '{"x":0,"y":0}',order_index INTEGER NOT NULL DEFAULT 0,article_search_queries TEXT NOT NULL DEFAULT '[]',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE roadmap_edges(id TEXT NOT NULL,workspace_id TEXT NOT NULL,roadmap_id TEXT NOT NULL,source_node_id TEXT NOT NULL,target_node_id TEXT NOT NULL,style TEXT NOT NULL DEFAULT 'solid',created_at TEXT NOT NULL,PRIMARY KEY(workspace_id,id));
CREATE TABLE tasks(id TEXT NOT NULL,workspace_id TEXT NOT NULL,project_id TEXT,roadmap_node_id TEXT,note_id TEXT,title TEXT NOT NULL,content TEXT NOT NULL DEFAULT '',status TEXT NOT NULL,priority INTEGER NOT NULL DEFAULT 0,due TEXT,planned_date TEXT,completed_at TEXT,done INTEGER NOT NULL DEFAULT 0,execution_type TEXT NOT NULL,horizon TEXT NOT NULL DEFAULT 'week',scope TEXT NOT NULL DEFAULT 'daily',sort_order INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,PRIMARY KEY(workspace_id,id));
CREATE TABLE task_recurrence_rules(task_id TEXT NOT NULL,workspace_id TEXT NOT NULL,start_date TEXT NOT NULL,end_date TEXT,frequency TEXT NOT NULL,"interval" INTEGER NOT NULL,weekdays TEXT,month_days TEXT,timezone TEXT NOT NULL,enabled INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,PRIMARY KEY(workspace_id,task_id));
CREATE TABLE task_occurrences(task_id TEXT NOT NULL,workspace_id TEXT NOT NULL,occurrence_date TEXT NOT NULL,occurrence_id TEXT,status TEXT NOT NULL,completed_at TEXT,note TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,PRIMARY KEY(workspace_id,task_id,occurrence_date));
CREATE TABLE events(id TEXT NOT NULL,workspace_id TEXT NOT NULL,title TEXT NOT NULL,start_time TEXT NOT NULL,end_time TEXT NOT NULL,location TEXT,kind TEXT,is_all_day INTEGER NOT NULL,notes TEXT,note_id TEXT,project_id TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,deleted_at TEXT,PRIMARY KEY(workspace_id,id));
INSERT INTO tenant_workspaces(workspace_id) VALUES('alpha');`
	if _, err := db.Exec(legacyDDL); err != nil {
		t.Fatalf("create legacy fixture: %v", err)
	}
	for _, migration := range []string{"0002_task_domain_v2.sql", "0003_task_domain_legacy_migration.sql"} {
		path := filepath.Join("..", "..", "db", "migrations", "tenant", "sqlite", migration)
		script, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", migration, err)
		}
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	return db
}

func seedCoordinatorLegacyTask(t *testing.T, db *sql.DB, title string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_projects(id,workspace_id,name,type,created_at,updated_at)
		VALUES('project-1','alpha','Project','personal','2026-07-01T00:00:00Z','2026-07-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks(id,workspace_id,project_id,title,content,status,priority,done,execution_type,horizon,scope,sort_order,created_at,updated_at)
		VALUES('task-1','alpha','project-1',?,'body','open',1,0,'single','week','daily',0,'2026-07-01T00:00:00Z','2026-07-01T00:00:00Z')`, title); err != nil {
		t.Fatal(err)
	}
}
