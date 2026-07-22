package contracttest

import (
	"database/sql"
	"fmt"
	"testing"
)

type TaskDomainV2Dialect string

const (
	TaskDomainV2Postgres TaskDomainV2Dialect = "postgres"
	TaskDomainV2SQLite   TaskDomainV2Dialect = "sqlite"
)

// RunTaskDomainV2SchemaSuite verifies invariants which must be enforced by
// both tenant database implementations. These are database contracts rather
// than repository tests: an application code path must not be able to bypass
// the workspace and state-machine boundaries.
func RunTaskDomainV2SchemaSuite(t *testing.T, db *sql.DB, dialect TaskDomainV2Dialect) {
	t.Helper()

	for _, table := range []string{
		"workspace_task_domain_state",
		"legacy_task_domain_entity_versions",
		"task_domain_legacy_outbox",
		"task_domain_legacy_id_map",
		"domain_projects_v2",
		"domain_learning_roadmaps_v2",
		"domain_roadmap_nodes_v2",
		"domain_roadmap_edges_v2",
		"domain_tasks_v2",
		"domain_task_dependencies_v2",
		"domain_task_schedules_v2",
		"domain_task_schedule_versions_v2",
		"domain_task_occurrences_v2",
		"domain_task_execution_logs_v2",
	} {
		t.Run("table_"+table, func(t *testing.T) {
			if _, err := db.Exec(`SELECT 1 FROM ` + table + ` WHERE 1=0`); err != nil {
				t.Fatalf("required shadow table %s is unavailable: %v", table, err)
			}
		})
	}

	t.Run("fresh_tenant_does_not_require_legacy_calendar_or_roadmap_tables", func(t *testing.T) {
		for _, legacyTable := range []string{"events", "task_occurrences", "task_recurrence_rules", "learning_roadmaps", "roadmap_nodes", "roadmap_edges"} {
			if tableExists(t, db, dialect, legacyTable) {
				t.Fatalf("fresh tenant migration must not synthesize legacy table %s", legacyTable)
			}
		}
	})

	t.Run("fresh_workspace_starts_on_v2", func(t *testing.T) {
		mustExec(t, db, `INSERT INTO tenant_workspaces(workspace_id) VALUES ('schema-fresh')`)
		var modelVersion string
		var acceptsLegacyWrites bool
		if err := db.QueryRow(`SELECT model_version,accept_legacy_writes FROM workspace_task_domain_state WHERE workspace_id='schema-fresh'`).Scan(&modelVersion, &acceptsLegacyWrites); err != nil {
			t.Fatalf("read provisioned domain state: %v", err)
		}
		if modelVersion != "v2" {
			t.Fatalf("fresh workspace model version = %q, want v2", modelVersion)
		}
		if acceptsLegacyWrites {
			t.Fatal("fresh v2 workspace unexpectedly accepts legacy writes")
		}
	})

	seedWorkspace(t, db, "schema-w1")
	seedWorkspace(t, db, "schema-w2")

	t.Run("workspace_state_checks", func(t *testing.T) {
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET model_version='v3' WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=FALSE WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET model_version='v2',accept_legacy_writes=TRUE WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET migration_state='backfilling',model_version='v2',migration_id='migration-1',accept_legacy_writes=FALSE WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET model_version='legacy',accept_legacy_writes=TRUE,v2_first_write_at=CURRENT_TIMESTAMP WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET migration_state='cutover',model_version='v2',accept_legacy_writes=FALSE WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET migration_state='backfilling',migration_id='' WHERE workspace_id='schema-w1'`)
		expectStatementRejected(t, db, `UPDATE workspace_task_domain_state SET migration_state='draining',accept_legacy_writes=FALSE,migration_id='migration-1',cutover_revision=5,source_watermark=6 WHERE workspace_id='schema-w1'`)
	})

	t.Run("legacy_outbox_envelope_checks", func(t *testing.T) {
		mustExec(t, db, `INSERT INTO legacy_task_domain_entity_versions
			(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)
			VALUES ('schema-w1','task','legacy-task-1',1,FALSE,CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO task_domain_legacy_outbox
			(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image,created_at)
			VALUES ('schema-w1','task','legacy-task-1','upsert',1,'{"id":"legacy-task-1"}',CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO task_domain_legacy_outbox
			(workspace_id,entity_kind,entity_id,operation,source_logical_version,tombstone_image,created_at)
			VALUES ('schema-w1','task','legacy-task-1','delete',2,'{"id":"legacy-task-1"}',CURRENT_TIMESTAMP)`)
		var outboxCount int
		var firstSequence, lastSequence int64
		if err := db.QueryRow(`SELECT COUNT(*), MIN(sequence), MAX(sequence)
			FROM task_domain_legacy_outbox WHERE workspace_id='schema-w1'`).Scan(&outboxCount, &firstSequence, &lastSequence); err != nil {
			t.Fatalf("read outbox sequences: %v", err)
		}
		if outboxCount != 2 || lastSequence <= firstSequence {
			t.Fatalf("outbox sequence count/range = %d/%d..%d, want two strictly ordered events", outboxCount, firstSequence, lastSequence)
		}

		mustExec(t, db, `INSERT INTO task_domain_legacy_id_map
			(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
			VALUES ('schema-w1','event','legacy-event-1','task','v2-task-1',1,FALSE,CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO task_domain_legacy_id_map
			(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
			VALUES ('schema-w1','event','legacy-event-1','occurrence','v2-occurrence-1',1,FALSE,CURRENT_TIMESTAMP)`)

		expectStatementRejected(t, db, `INSERT INTO legacy_task_domain_entity_versions
			(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)
			VALUES ('schema-w1','unknown','bad-kind',1,FALSE,CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO legacy_task_domain_entity_versions
			(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)
			VALUES ('schema-w1','task','bad-version',0,FALSE,CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO task_domain_legacy_outbox
			(workspace_id,entity_kind,entity_id,operation,source_logical_version,created_at)
			VALUES ('schema-w1','task','missing-image','upsert',1,CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO task_domain_legacy_outbox
			(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image,created_at)
			VALUES ('schema-w1','task','both-images','delete',1,'{}','{}',CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO task_domain_legacy_outbox
			(workspace_id,entity_kind,entity_id,operation,source_logical_version,row_image,created_at)
			VALUES ('schema-w1','task','array-image','upsert',1,'[]',CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO task_domain_legacy_id_map
			(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
			VALUES ('schema-w2','event','legacy-event-1','occurrence','v2-occurrence-1',0,FALSE,CURRENT_TIMESTAMP)`)
	})

	t.Run("project_checks_and_system_role_uniqueness", func(t *testing.T) {
		insertProject(t, db, "schema-w1", "p1", "Project 1", "standard", "personal")
		expectStatementRejected(t, db, projectInsertSQL("schema-w1", "p2", "Project 2", "standard", "personal"))
		expectStatementRejected(t, db, `INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at)
			VALUES ('schema-w1','p-bad-status','Bad status','standard','short','unknown',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	})

	t.Run("roadmap_and_task_require_same_workspace_and_project", func(t *testing.T) {
		insertProject(t, db, "schema-w1", "learn", "Learning", "learning", "")
		insertProject(t, db, "schema-w1", "other", "Other", "standard", "")
		mustExec(t, db, `INSERT INTO domain_learning_roadmaps_v2
			(workspace_id,id,project_id,status,revision,created_at,updated_at)
			VALUES ('schema-w1','roadmap-1','learn','active',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
		mustExec(t, db, `INSERT INTO domain_roadmap_nodes_v2
			(workspace_id,id,project_id,roadmap_id,title,node_type,status,position,revision,created_at,updated_at)
			VALUES ('schema-w1','node-1','learn','roadmap-1','Node','topic','available',0,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, taskInsertSQL("schema-w1", "cross-project", "other", "node-1", "active"))
		expectStatementRejected(t, db, taskInsertSQL("schema-w2", "cross-workspace", "learn", "", "active"))
		mustExec(t, db, taskInsertSQL("schema-w1", "task-1", "learn", "node-1", "active"))
		expectStatementRejected(t, db, taskInsertSQL("schema-w1", "task-bad", "learn", "", "unknown"))
	})

	t.Run("dependency_identity_and_kind_checks", func(t *testing.T) {
		mustExec(t, db, taskInsertSQL("schema-w1", "task-2", "learn", "", "active"))
		mustExec(t, db, `INSERT INTO domain_task_dependencies_v2
			(workspace_id,predecessor_task_id,successor_task_id,dependency_type,created_at)
			VALUES ('schema-w1','task-1','task-2','finish_to_start',CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO domain_task_dependencies_v2
			(workspace_id,predecessor_task_id,successor_task_id,dependency_type,created_at)
			VALUES ('schema-w1','task-1','task-1','related',CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `INSERT INTO domain_task_dependencies_v2
			(workspace_id,predecessor_task_id,successor_task_id,dependency_type,created_at)
			VALUES ('schema-w2','task-1','task-2','related',CURRENT_TIMESTAMP)`)
	})

	t.Run("schedule_state_time_and_effective_range_checks", func(t *testing.T) {
		createSchedule(t, db, "schema-w1", "task-1", 2, []string{
			scheduleVersionInsertSQL("schema-w1", "task-1", 1, "2026-01-01", "2026-02-01", "daily", "date", "2026-01-01", "", 0),
			scheduleVersionInsertSQL("schema-w1", "task-1", 2, "2026-02-01", "", "daily", "date", "2026-02-01", "", 0),
		})

		expectStatementRejected(t, db, scheduleVersionInsertSQL("schema-w1", "task-1", 3, "2026-01-15", "2026-03-01", "daily", "date", "2026-01-15", "", 0))
		expectStatementRejected(t, db, scheduleVersionInsertSQL("schema-w1", "task-1", 4, "2026-03-01", "", "daily", "date", "2026-03-01", "", 0))
		expectStatementRejected(t, db, scheduleVersionInsertSQL("schema-w1", "task-1", 5, "2026-04-01", "2026-03-01", "daily", "date", "2026-04-01", "", 0))

		expectTransactionRejected(t, db, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE domain_task_schedules_v2 SET current_schedule_revision=1 WHERE workspace_id='schema-w1' AND task_id='task-1'`)
			return err
		})

		expectTransactionRejected(t, db, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO domain_task_schedules_v2
				(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
				VALUES ('schema-w1','task-2',1,1,'not-a-state',CURRENT_TIMESTAMP)`); err != nil {
				return err
			}
			return nil
		})

		mustExec(t, db, taskInsertSQL("schema-w1", "task-3", "learn", "", "active"))
		expectTransactionRejected(t, db, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO domain_task_schedules_v2
				(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
				VALUES ('schema-w1','task-3',1,1,'idle',CURRENT_TIMESTAMP)`); err != nil {
				return err
			}
			_, err := tx.Exec(scheduleVersionInsertSQL("schema-w1", "task-3", 1, "2026-01-01", "", "none", "time_block", "2026-01-01", "", 0))
			return err
		})
	})

	t.Run("recurrence_rule_shape_checks", func(t *testing.T) {
		accepted := []struct {
			name           string
			recurrenceType string
			rule           string
		}{
			{name: "none_empty_object", recurrenceType: "none", rule: `{}`},
			{name: "none_whitespace_empty_object", recurrenceType: "none", rule: `{ }`},
			{name: "daily_positive_integer_interval", recurrenceType: "daily", rule: `{"interval":1}`},
			{name: "weekly_unique_weekdays", recurrenceType: "weekly", rule: `{"interval":2,"weekdays":[0,2,6]}`},
			{name: "monthly_unique_month_days", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[1,15,31]}`},
		}
		for _, tc := range accepted {
			t.Run("accept_"+tc.name, func(t *testing.T) {
				assertScheduleRuleAccepted(t, db, "rule-ok-"+tc.name, tc.recurrenceType, tc.rule)
			})
		}

		rejected := []struct {
			name           string
			recurrenceType string
			rule           string
		}{
			{name: "none_non_empty", recurrenceType: "none", rule: `{"interval":1}`},
			{name: "daily_missing_interval", recurrenceType: "daily", rule: `{}`},
			{name: "daily_zero_interval", recurrenceType: "daily", rule: `{"interval":0}`},
			{name: "daily_fractional_interval", recurrenceType: "daily", rule: `{"interval":1.5}`},
			{name: "daily_weekdays", recurrenceType: "daily", rule: `{"interval":1,"weekdays":[1]}`},
			{name: "daily_month_days", recurrenceType: "daily", rule: `{"interval":1,"month_days":[1]}`},
			{name: "daily_unknown_field", recurrenceType: "daily", rule: `{"interval":1,"extra":true}`},
			{name: "weekly_missing_interval", recurrenceType: "weekly", rule: `{"weekdays":[1]}`},
			{name: "weekly_empty_weekdays", recurrenceType: "weekly", rule: `{"interval":1,"weekdays":[]}`},
			{name: "weekly_duplicate_weekdays", recurrenceType: "weekly", rule: `{"interval":1,"weekdays":[1,1]}`},
			{name: "weekly_out_of_range_weekday", recurrenceType: "weekly", rule: `{"interval":1,"weekdays":[7]}`},
			{name: "weekly_month_days", recurrenceType: "weekly", rule: `{"interval":1,"weekdays":[1],"month_days":[1]}`},
			{name: "weekly_unknown_field", recurrenceType: "weekly", rule: `{"interval":1,"weekdays":[1],"extra":true}`},
			{name: "monthly_missing_interval", recurrenceType: "monthly", rule: `{"month_days":[1]}`},
			{name: "monthly_empty_month_days", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[]}`},
			{name: "monthly_duplicate_month_days", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[1,1]}`},
			{name: "monthly_out_of_range_month_day", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[32]}`},
			{name: "monthly_weekdays", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[1],"weekdays":[1]}`},
			{name: "monthly_unknown_field", recurrenceType: "monthly", rule: `{"interval":1,"month_days":[1],"extra":true}`},
		}
		for _, tc := range rejected {
			t.Run("reject_"+tc.name, func(t *testing.T) {
				assertScheduleRuleRejected(t, db, "rule-bad-"+tc.name, tc.recurrenceType, tc.rule)
			})
		}
	})

	t.Run("occurrence_status_timing_and_blocked_metadata_checks", func(t *testing.T) {
		expectStatementRejected(t, db, occurrenceInsertSQL("occ-done-bad", "done", "NULL", "NULL", "NULL"))
		expectStatementRejected(t, db, occurrenceInsertSQL("occ-completed-bad", "active", "CURRENT_TIMESTAMP", "NULL", "NULL"))
		expectStatementRejected(t, db, occurrenceInsertSQL("occ-blocked-bad", "blocked", "NULL", "NULL", "NULL"))
		expectStatementRejected(t, db, occurrenceInsertSQL("occ-open-metadata-bad", "open", "NULL", "'stale reason'", "'stale action'"))
		expectStatementRejected(t, db, occurrenceInsertSQL("occ-status-bad", "invalid", "NULL", "NULL", "NULL"))
		expectStatementRejected(t, db, `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,execution_status,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES ('schema-w1','occ-date-with-time','task-1','occ-date-with-time','2026-02-02','2026-02-02T09:00:00Z','2026-02-02T10:00:00Z','open',1,2,FALSE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
		mustExec(t, db, occurrenceInsertSQL("occ-1", "blocked", "NULL", "'Waiting for input'", "'Ask owner'"))
	})

	t.Run("execution_logs_are_immutable", func(t *testing.T) {
		mustExec(t, db, `INSERT INTO domain_task_execution_logs_v2
			(workspace_id,id,occurrence_id,from_status,to_status,blocked_reason,next_action,actor_id,metadata,created_at)
			VALUES ('schema-w1','log-1','occ-1','active','blocked','Waiting for input','Ask owner','user-1','{}',CURRENT_TIMESTAMP)`)
		expectStatementRejected(t, db, `UPDATE domain_task_execution_logs_v2 SET actor_id='user-2' WHERE workspace_id='schema-w1' AND id='log-1'`)
		expectStatementRejected(t, db, `DELETE FROM domain_task_execution_logs_v2 WHERE workspace_id='schema-w1' AND id='log-1'`)
		expectStatementRejected(t, db, `INSERT INTO domain_task_execution_logs_v2
			(workspace_id,id,occurrence_id,from_status,to_status,actor_id,metadata,created_at)
			VALUES ('schema-w1','log-bad','occ-1','active','blocked','user-1','{}',CURRENT_TIMESTAMP)`)
	})
}

func tableExists(t *testing.T, db *sql.DB, dialect TaskDomainV2Dialect, table string) bool {
	t.Helper()
	var count int
	var err error
	if dialect == TaskDomainV2Postgres {
		err = db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1`, table).Scan(&count)
	} else {
		err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	}
	if err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	return count == 1
}

func seedWorkspace(t *testing.T, db *sql.DB, workspaceID string) {
	t.Helper()
	mustExec(t, db, fmt.Sprintf(`INSERT INTO tenant_workspaces(workspace_id) VALUES ('%s')`, workspaceID))
	mustExec(t, db, fmt.Sprintf(`UPDATE workspace_task_domain_state
		SET model_version='legacy', migration_state='idle', source_watermark=0, write_epoch=1,
			accept_legacy_writes=TRUE, migration_timezone='UTC', revision=1, updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id='%s'`, workspaceID))
}

func insertProject(t *testing.T, db *sql.DB, workspaceID, id, name, kind, systemRole string) {
	t.Helper()
	mustExec(t, db, projectInsertSQL(workspaceID, id, name, kind, systemRole))
}

func projectInsertSQL(workspaceID, id, name, kind, systemRole string) string {
	role := "NULL"
	if systemRole != "" {
		role = "'" + systemRole + "'"
	}
	return fmt.Sprintf(`INSERT INTO domain_projects_v2
		(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
		VALUES ('%s','%s','%s','%s','short','active',%s,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, workspaceID, id, name, kind, role)
}

func taskInsertSQL(workspaceID, id, projectID, roadmapNodeID, status string) string {
	node := "NULL"
	if roadmapNodeID != "" {
		node = "'" + roadmapNodeID + "'"
	}
	return fmt.Sprintf(`INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,roadmap_node_id,title,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES ('%s','%s','%s',%s,'Task %s','%s',0,0,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, workspaceID, id, projectID, node, id, status)
}

func scheduleVersionInsertSQL(workspaceID, taskID string, revision int, effectiveFrom, effectiveTo, recurrenceType, timingType, startsOn, localStart string, duration int) string {
	dateOrNull := func(value string) string {
		if value == "" {
			return "NULL"
		}
		return "'" + value + "'"
	}
	timeOrNull := dateOrNull(localStart)
	durationValue := "NULL"
	if duration != 0 {
		durationValue = fmt.Sprintf("%d", duration)
	}
	recurrenceRule := `{}`
	switch recurrenceType {
	case "daily":
		recurrenceRule = `{"interval":1}`
	case "weekly":
		recurrenceRule = `{"interval":1,"weekdays":[1]}`
	case "monthly":
		recurrenceRule = `{"interval":1,"month_days":[1]}`
	}
	return fmt.Sprintf(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES ('%s','%s',%d,%s,%s,'%s','%s','UTC',%s,'%s',%s,%s,CURRENT_TIMESTAMP)`,
		workspaceID, taskID, revision, dateOrNull(effectiveFrom), dateOrNull(effectiveTo), recurrenceType, timingType, dateOrNull(startsOn), recurrenceRule, timeOrNull, durationValue)
}

func createSchedule(t *testing.T, db *sql.DB, workspaceID, taskID string, currentRevision int, versions []string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES ('%s','%s',1,%d,'idle',CURRENT_TIMESTAMP)`, workspaceID, taskID, currentRevision)); err != nil {
		t.Fatalf("insert schedule header: %v", err)
	}
	for _, statement := range versions {
		if _, err := tx.Exec(statement); err != nil {
			t.Fatalf("insert schedule version: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit schedule: %v", err)
	}
}

func assertScheduleRuleAccepted(t *testing.T, db *sql.DB, taskID, recurrenceType, rule string) {
	t.Helper()
	mustExec(t, db, taskInsertSQL("schema-w1", taskID, "learn", "", "active"))
	if err := transactScheduleRule(db, taskID, recurrenceType, rule); err != nil {
		t.Fatalf("expected recurrence rule to be accepted: %v", err)
	}
}

func assertScheduleRuleRejected(t *testing.T, db *sql.DB, taskID, recurrenceType, rule string) {
	t.Helper()
	mustExec(t, db, taskInsertSQL("schema-w1", taskID, "learn", "", "active"))
	if err := transactScheduleRule(db, taskID, recurrenceType, rule); err == nil {
		t.Fatalf("expected recurrence rule to be rejected: type=%s rule=%s", recurrenceType, rule)
	}
}

func transactScheduleRule(db *sql.DB, taskID, recurrenceType, rule string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES ('schema-w1','%s',1,1,'idle',CURRENT_TIMESTAMP)`, taskID)); err != nil {
		return err
	}
	effectiveFrom := "'2026-01-01'"
	startsOn := "'2026-01-01'"
	timingType := "date"
	if recurrenceType == "none" {
		effectiveFrom = "NULL"
		startsOn = "NULL"
		timingType = "unscheduled"
	}
	if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,recurrence_type,timing_type,timezone,starts_on,recurrence_rule,created_at)
		VALUES ('schema-w1','%s',1,%s,'%s','%s','UTC',%s,'%s',CURRENT_TIMESTAMP)`,
		taskID, effectiveFrom, recurrenceType, timingType, startsOn, rule)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func occurrenceInsertSQL(id, status, completedAt, blockedReason, nextAction string) string {
	return fmt.Sprintf(`INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,execution_status,completed_at,blocked_reason,next_action,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
		VALUES ('schema-w1','%s','task-1','%s','2026-02-02','%s',%s,%s,%s,1,2,FALSE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		id, id, status, completedAt, blockedReason, nextAction)
}

func mustExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("execute statement: %v\n%s", err, statement)
	}
}

func expectStatementRejected(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err == nil {
		t.Fatalf("expected database to reject statement:\n%s", statement)
	}
}

func expectTransactionRejected(t *testing.T, db *sql.DB, apply func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("expected transaction commit to be rejected")
	}
}
