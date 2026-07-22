package taskmigration

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReconcileStoreSQLiteObservesCanonicalProjectionFromRealRows(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	input := projectionWriterInput("alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	writeProjectionTransaction(t, db, writer, input)

	store, err := NewReconcileStore(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := store.Observe(context.Background(), input.WorkspaceID, input.Projection, input.SourceVersions)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !observation.Plan.Ready || len(observation.Input.Source) != 4 || len(observation.Input.V2) != 4 || len(observation.Input.IDMaps) != 4 {
		t.Fatalf("observation=%+v", observation)
	}
	for _, row := range observation.Input.Source {
		if row.Digest == "" {
			t.Fatalf("empty canonical digest for %+v", row.Target)
		}
	}
}

func TestReconcileStoreSQLiteRepairsMissingTargetsButRequiresASecondObservation(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	input := projectionWriterInput("alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, input)
	if _, err := db.Exec(`DELETE FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND id='occurrence-1'`); err != nil {
		t.Fatal(err)
	}

	store, _ := NewReconcileStore(db, DialectSQLite)
	before, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	if before.Plan.Ready || len(before.Plan.UpsertMissing) != 1 || before.Plan.UpsertMissing[0].Target != replayKey(ReplayEntityOccurrence, "occurrence-1") {
		t.Fatalf("before plan=%+v", before.Plan)
	}
	result, err := store.ApplyPlan(context.Background(), before, input.WrittenAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	if !result.RequiresObservation || result.Ready {
		t.Fatalf("apply result=%+v", result)
	}
	after, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Plan.Ready {
		t.Fatalf("after plan=%+v", after.Plan)
	}
}

func TestReconcileStoreSQLiteDeletesOnlyMappedTargetsWithDurableDeletedSource(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	base := projectionWriterInput("alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, base)
	extra := extraReconcileProjection("alpha")
	writeProjectionTransaction(t, db, writer, extra)
	if _, err := db.Exec(`UPDATE legacy_task_domain_entity_versions SET deleted=1,logical_version=logical_version+1
		WHERE workspace_id='alpha' AND entity_kind='event' AND entity_id='legacy-event-extra'`); err != nil {
		t.Fatal(err)
	}

	store, _ := NewReconcileStore(db, DialectSQLite)
	before, err := store.Observe(context.Background(), "alpha", base.Projection, base.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	if got := mutationTargets(before.Plan.DeleteExtra); len(got) != 3 || got[0].Kind != ReplayEntityOccurrence || got[1].Kind != ReplayEntitySchedule || got[2].Kind != ReplayEntityTask {
		t.Fatalf("delete order=%v", got)
	}
	if _, err := store.ApplyPlan(context.Background(), before, base.WrittenAt.Add(time.Hour)); err != nil {
		t.Fatalf("ApplyPlan: %v", err)
	}
	after, err := store.Observe(context.Background(), "alpha", base.Projection, base.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Plan.Ready {
		t.Fatalf("after plan=%+v", after.Plan)
	}
	for _, target := range []struct{ table, column, id string }{
		{"domain_tasks_v2", "id", "extra-task"},
		{"domain_task_schedules_v2", "task_id", "extra-task"},
		{"domain_task_occurrences_v2", "id", "extra-occurrence"},
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+target.table+` WHERE workspace_id='alpha' AND `+target.column+`=?`, target.id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s extra rows=%d", target.table, count)
		}
	}
}

func TestReconcileStoreSQLiteRefusesDeleteWhenSourceLedgerIsNotDeleted(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	base := projectionWriterInput("alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, base)
	extra := extraReconcileProjection("alpha")
	writeProjectionTransaction(t, db, writer, extra)

	store, _ := NewReconcileStore(db, DialectSQLite)
	before, err := store.Observe(context.Background(), "alpha", base.Projection, base.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ApplyPlan(context.Background(), before, base.WrittenAt.Add(time.Hour))
	var block *ReconcileApplyBlock
	if !errors.As(err, &block) || block.Code != ReconcileApplySourceNotDeleted {
		t.Fatalf("ApplyPlan error=%v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='extra-task'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("extra task count=%d err=%v", count, err)
	}
}

func TestReconcileStoreSQLiteRejectsStaleObservationBeforeAnyRepair(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	input := projectionWriterInput("alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, input)
	if _, err := db.Exec(`DELETE FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND id='occurrence-1'`); err != nil {
		t.Fatal(err)
	}
	store, _ := NewReconcileStore(db, DialectSQLite)
	observation, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent coordinator repairs the row before this observation is
	// applied. The stale caller must not replay its old plan.
	writeProjectionTransaction(t, db, writer, input)
	_, err = store.ApplyPlan(context.Background(), observation, input.WrittenAt.Add(time.Hour))
	var block *ReconcileApplyBlock
	if !errors.As(err, &block) || block.Code != ReconcileApplyStaleObservation {
		t.Fatalf("ApplyPlan error=%v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND id='occurrence-1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("occurrence count=%d err=%v", count, err)
	}
}

func TestNewReconcileStoreRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewReconcileStore(nil, DialectSQLite); !errors.Is(err, ErrInvalidReconcileStoreInput) {
		t.Fatalf("nil database error=%v", err)
	}
	db := openProjectionWriterSQLite(t, "alpha")
	if _, err := NewReconcileStore(db, Dialect("oracle")); !errors.Is(err, ErrInvalidReconcileStoreInput) {
		t.Fatalf("invalid dialect error=%v", err)
	}
}

func TestReconcileStoreSQLiteFailsClosedOnDigestOrStatusCorruption(t *testing.T) {
	t.Run("digest mismatch", func(t *testing.T) {
		db := openProjectionWriterSQLite(t, "alpha")
		input := projectionWriterInput("alpha")
		writer, _ := NewV2ProjectionWriter(DialectSQLite)
		writeProjectionTransaction(t, db, writer, input)
		if _, err := db.Exec(`UPDATE domain_tasks_v2 SET title='tampered' WHERE workspace_id='alpha' AND id='task-1'`); err != nil {
			t.Fatal(err)
		}
		store, _ := NewReconcileStore(db, DialectSQLite)
		observation, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.ApplyPlan(context.Background(), observation, input.WrittenAt.Add(time.Hour))
		var block *ReconcileApplyBlock
		if !errors.As(err, &block) || block.Code != ReconcileApplyUnsafeMismatch {
			t.Fatalf("ApplyPlan error=%v", err)
		}
	})

	t.Run("status invariant", func(t *testing.T) {
		db := openProjectionWriterSQLite(t, "alpha")
		input := projectionWriterInput("alpha")
		writer, _ := NewV2ProjectionWriter(DialectSQLite)
		writeProjectionTransaction(t, db, writer, input)
		if _, err := db.Exec(`PRAGMA ignore_check_constraints=ON;
			UPDATE domain_task_occurrences_v2 SET execution_status='done',completed_at=NULL
			WHERE workspace_id='alpha' AND id='occurrence-1';
			PRAGMA ignore_check_constraints=OFF;`); err != nil {
			t.Fatal(err)
		}
		store, _ := NewReconcileStore(db, DialectSQLite)
		observation, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
		if err != nil {
			t.Fatal(err)
		}
		if !hasMismatchCode(observation.Plan.Mismatches, ReconcileMismatchStatus) {
			t.Fatalf("mismatches=%+v", observation.Plan.Mismatches)
		}
		_, err = store.ApplyPlan(context.Background(), observation, input.WrittenAt.Add(time.Hour))
		var block *ReconcileApplyBlock
		if !errors.As(err, &block) || block.Code != ReconcileApplyUnsafeMismatch {
			t.Fatalf("ApplyPlan error=%v", err)
		}
	})

	t.Run("foreign key invariant", func(t *testing.T) {
		db := openProjectionWriterSQLite(t, "alpha")
		input := projectionWriterInput("alpha")
		writer, _ := NewV2ProjectionWriter(DialectSQLite)
		writeProjectionTransaction(t, db, writer, input)
		if _, err := db.Exec(`PRAGMA foreign_keys=OFF;
			DELETE FROM domain_projects_v2 WHERE workspace_id='alpha' AND id='project-1';
			PRAGMA foreign_keys=ON;`); err != nil {
			t.Fatal(err)
		}
		store, _ := NewReconcileStore(db, DialectSQLite)
		observation, err := store.Observe(context.Background(), "alpha", input.Projection, input.SourceVersions)
		if err != nil {
			t.Fatal(err)
		}
		if !hasMismatchCode(observation.Plan.Mismatches, ReconcileMismatchForeignKey) {
			t.Fatalf("mismatches=%+v", observation.Plan.Mismatches)
		}
		_, err = store.ApplyPlan(context.Background(), observation, input.WrittenAt.Add(time.Hour))
		var block *ReconcileApplyBlock
		if !errors.As(err, &block) || block.Code != ReconcileApplyUnsafeMismatch {
			t.Fatalf("ApplyPlan error=%v", err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM domain_projects_v2 WHERE workspace_id='alpha' AND id='project-1'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("project unexpectedly repaired count=%d err=%v", count, err)
		}
	})
}

func TestReconcileStoreSQLiteNeverDeletesGeneratedSystemProject(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	base := projectionWriterInput("alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	writeProjectionTransaction(t, db, writer, base)
	if _, err := db.Exec(`INSERT INTO legacy_task_domain_entity_versions(workspace_id,entity_kind,entity_id,logical_version,deleted)
		VALUES('alpha','project','legacy-system',2,1);
		INSERT INTO domain_projects_v2(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
		VALUES('alpha','system-inbox','Inbox','standard','short','active','inbox',1,'2026-07-22T00:00:00Z','2026-07-22T00:00:00Z');
		INSERT INTO task_domain_legacy_id_map(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
		VALUES('alpha','project','legacy-system','project','system-inbox',1,0,'2026-07-22T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	store, _ := NewReconcileStore(db, DialectSQLite)
	observation, err := store.Observe(context.Background(), "alpha", base.Projection, base.SourceVersions)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Plan.Ready || containsMutationTarget(observation.Plan.DeleteExtra, replayKey(ReplayEntityProject, "system-inbox")) {
		t.Fatalf("plan=%+v", observation.Plan)
	}
}

func extraReconcileProjection(workspaceID string) V2ProjectionWrite {
	return V2ProjectionWrite{
		WorkspaceID:    workspaceID,
		WrittenAt:      time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC),
		SourceVersions: []ProjectionSourceVersion{{EntityKind: LegacyEntityEvent, LegacyID: "legacy-event-extra", LogicalVersion: 1}},
		Projection: V2Projection{
			Projects:    []V2ProjectProjection{{ID: "project-1", Name: "Projection", Kind: "standard", Horizon: "short"}},
			Tasks:       []V2TaskProjection{{ID: "extra-task", ProjectID: "project-1", Title: "Extra", LifecycleStatus: "active"}},
			Schedules:   []V2ScheduleProjection{{TaskID: "extra-task", RecurrenceType: "none", TimingType: "unscheduled", Timezone: "UTC", Interval: 1}},
			Occurrences: []V2OccurrenceProjection{{ID: "extra-occurrence", TaskID: "extra-task", OccurrenceKey: "once", ExecutionStatus: "open", GeneratedScheduleRevision: 1}},
			IDMap:       []V2IDMapEntry{{LegacyKind: LegacyEntityEvent, LegacyID: "legacy-event-extra", TargetTaskID: "extra-task", TargetScheduleID: "extra-task", TargetOccurrenceID: "extra-occurrence"}},
		},
	}
}

func hasMismatchCode(mismatches []ReconcileMismatch, code ReconcileMismatchCode) bool {
	for _, mismatch := range mismatches {
		if mismatch.Code == code {
			return true
		}
	}
	return false
}
