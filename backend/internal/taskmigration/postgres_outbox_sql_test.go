package taskmigration

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderPostgresLegacyOutboxSQLFreshV2IsEmpty(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(SchemaInventory{Mode: TaskDomainSourceFreshV2})
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	got, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxSQL: %v", err)
	}
	if got != "" {
		t.Fatalf("fresh-v2 SQL = %q, want empty", got)
	}
}

func TestRenderPostgresLegacyOutboxBaselineSQLLocksSourcesAndPreservesExistingVersions(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	lockSQL, err := RenderPostgresLegacyOutboxLockSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxLockSQL: %v", err)
	}
	if !strings.HasPrefix(lockSQL, `LOCK TABLE "task_projects", "tasks", "task_recurrence_rules", "task_occurrences", "events", "learning_roadmaps", "roadmap_nodes", "roadmap_edges" IN SHARE ROW EXCLUSIVE MODE;`) {
		t.Fatalf("PostgreSQL install does not lock every source before DDL/seed: %s", lockSQL)
	}

	got, err := RenderPostgresLegacyOutboxBaselineSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxBaselineSQL: %v", err)
	}
	assertSQLCount(t, got, "INSERT INTO legacy_task_domain_entity_versions", len(LegacyOutboxManifest())+3)
	assertSQLCount(t, got, "DO NOTHING", len(LegacyOutboxManifest())+3)
	if strings.Contains(got, "DO UPDATE") {
		t.Fatalf("baseline SQL can reset existing logical versions:\n%s", got)
	}
	wantOccurrenceIdentity := `jsonb_build_array(to_jsonb(source."task_id"), to_jsonb(source."occurrence_date"))::text`
	if !strings.Contains(got, wantOccurrenceIdentity) {
		t.Fatalf("baseline occurrence identity does not match trigger identity: %s", got)
	}
}

func TestRenderPostgresLegacyOutboxSQLCoversEverySourceDML(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	sqlText, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxSQL: %v", err)
	}
	wantTriggers := len(LegacyOutboxManifest())*3 + len(LegacyRoadmapFreezeTriggerManifest())
	if got := strings.Count(sqlText, "CREATE TRIGGER "); got != wantTriggers {
		t.Fatalf("CREATE TRIGGER count = %d, want %d\n%s", got, wantTriggers, sqlText)
	}
	if got := strings.Count(sqlText, "EXECUTE PROCEDURE "); got != wantTriggers || strings.Contains(sqlText, "EXECUTE FUNCTION ") {
		t.Fatalf("PostgreSQL 10 compatible trigger execution count = %d, want %d and no EXECUTE FUNCTION", got, wantTriggers)
	}
	if got := strings.Count(sqlText, "CREATE OR REPLACE FUNCTION "); got != wantTriggers {
		t.Fatalf("function count = %d, want %d", got, wantTriggers)
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

func TestRenderPostgresLegacyOutboxSQLPreservesFenceLedgerAndImages(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	sqlText, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("RenderPostgresLegacyOutboxSQL: %v", err)
	}

	standardTriggers := len(LegacyOutboxManifest()) * 3
	allTriggers := standardTriggers + len(LegacyRoadmapFreezeTriggerManifest())
	assertSQLCount(t, sqlText, "FROM workspace_task_domain_state", allTriggers)
	assertSQLCount(t, sqlText, "FOR SHARE;", allTriggers)
	assertSQLCount(t, sqlText, "legacy_task_domain_fenced", 15)
	assertSQLCount(t, sqlText, "INSERT INTO legacy_task_domain_entity_versions", allTriggers)
	assertSQLCount(t, sqlText, "logical_version = legacy_task_domain_entity_versions.logical_version + 1", 15)
	assertSQLCount(t, sqlText, "RETURNING logical_version INTO v_logical_version;", 15)
	assertSQLCount(t, sqlText, "INSERT INTO task_domain_legacy_outbox", 15)
	if !strings.Contains(sqlText, `'start_time', NEW."start_at"`) || !strings.Contains(sqlText, `'end_time', OLD."end_at"`) {
		t.Fatalf("PostgreSQL event row images do not canonicalize start_at/end_at: %s", sqlText)
	}
	assertSQLCount(t, sqlText, "'upsert'", 10)
	assertSQLCount(t, sqlText, "'delete'", 5)

	for _, operation := range []string{"insert", "update"} {
		body := postgresTriggerFunctionBody(t, sqlText, "task_occurrences", operation)
		wantIdentity := `jsonb_build_array(to_jsonb(NEW."task_id"), to_jsonb(NEW."occurrence_date"))::text`
		if !strings.Contains(body, wantIdentity) {
			t.Errorf("%s occurrence identity is not canonical JSON: %s", operation, body)
		}
	}
	deleteBody := postgresTriggerFunctionBody(t, sqlText, "task_occurrences", "delete")
	wantDeleteIdentity := `jsonb_build_array(to_jsonb(OLD."task_id"), to_jsonb(OLD."occurrence_date"))::text`
	if !strings.Contains(deleteBody, wantDeleteIdentity) {
		t.Errorf("delete occurrence identity is not canonical JSON: %s", deleteBody)
	}
}

func TestRenderPostgresLegacyOutboxSQLIsDeterministic(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}

	first, err := RenderPostgresLegacyOutboxSQL(plan)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	for iteration := 0; iteration < 10; iteration++ {
		got, renderErr := RenderPostgresLegacyOutboxSQL(plan)
		if renderErr != nil {
			t.Fatalf("render %d: %v", iteration, renderErr)
		}
		if got != first {
			t.Fatalf("render %d changed output", iteration)
		}
	}
}

func TestRenderPostgresLegacyOutboxSQLRejectsTamperedPlans(t *testing.T) {
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
			name: "injected identity column",
			mutate: func(plan TriggerPlan) TriggerPlan {
				plan.Upserts[0].IdentityColumns[0] = `id")::text; SELECT pg_sleep(10); --`
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
			got, renderErr := RenderPostgresLegacyOutboxSQL(test.mutate(plan))
			if !errors.Is(renderErr, ErrInvalidPostgresLegacyOutboxPlan) {
				t.Fatalf("error = %v, want ErrInvalidPostgresLegacyOutboxPlan", renderErr)
			}
			if got != "" {
				t.Fatalf("rejected plan rendered SQL: %q", got)
			}
		})
	}

	fresh := TriggerPlan{Mode: TaskDomainSourceFreshV2, Upserts: validPlan.Upserts[:1]}
	if got, renderErr := RenderPostgresLegacyOutboxSQL(fresh); !errors.Is(renderErr, ErrInvalidPostgresLegacyOutboxPlan) || got != "" {
		t.Fatalf("contaminated fresh plan = (%q, %v)", got, renderErr)
	}
	if got, renderErr := RenderPostgresLegacyOutboxSQL(TriggerPlan{Mode: "unknown"}); !errors.Is(renderErr, ErrInvalidPostgresLegacyOutboxPlan) || got != "" {
		t.Fatalf("unknown-mode plan = (%q, %v)", got, renderErr)
	}
}

func assertSQLCount(t *testing.T, sqlText, fragment string, want int) {
	t.Helper()
	if got := strings.Count(sqlText, fragment); got != want {
		t.Fatalf("count(%q) = %d, want %d", fragment, got, want)
	}
}

func postgresTriggerFunctionBody(t *testing.T, sqlText, table, operation string) string {
	t.Helper()
	name := `"task_domain_legacy_outbox_` + table + `_` + operation + `"`
	start := strings.Index(sqlText, "CREATE OR REPLACE FUNCTION "+name)
	if start < 0 {
		t.Fatalf("function %s not found", name)
	}
	endRelative := strings.Index(sqlText[start:], "$task_domain_outbox$;")
	if endRelative < 0 {
		t.Fatalf("function %s terminator not found", name)
	}
	return sqlText[start : start+endRelative]
}
