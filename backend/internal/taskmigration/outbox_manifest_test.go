package taskmigration

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyOutboxManifestIsCompleteAndDependencyOrdered(t *testing.T) {
	manifest := LegacyOutboxManifest()
	want := []struct {
		kind      ReplayEntityKind
		table     string
		identity  []string
		dependsOn []ReplayEntityKind
	}{
		{ReplayEntityProject, "task_projects", []string{"id"}, nil},
		{ReplayEntityTask, "tasks", []string{"id"}, []ReplayEntityKind{ReplayEntityProject}},
		{ReplayEntityRule, "task_recurrence_rules", []string{"task_id"}, []ReplayEntityKind{ReplayEntityTask}},
		{ReplayEntityOccurrence, "task_occurrences", []string{"task_id", "occurrence_date"}, []ReplayEntityKind{ReplayEntityTask, ReplayEntityRule}},
		{ReplayEntityEvent, "events", []string{"id"}, []ReplayEntityKind{ReplayEntityProject}},
	}
	if len(manifest) != len(want) {
		t.Fatalf("manifest entries = %d, want %d: %#v", len(manifest), len(want), manifest)
	}
	for index, expected := range want {
		entry := manifest[index]
		if entry.EntityKind != expected.kind || entry.Table != expected.table || entry.WorkspaceColumn != "workspace_id" {
			t.Fatalf("manifest[%d] identity = %#v", index, entry)
		}
		if entry.DependencyOrder != index {
			t.Fatalf("manifest[%d].DependencyOrder = %d, want %d", index, entry.DependencyOrder, index)
		}
		if !reflect.DeepEqual(entry.IdentityColumns, expected.identity) {
			t.Fatalf("manifest[%d].IdentityColumns = %#v, want %#v", index, entry.IdentityColumns, expected.identity)
		}
		if !reflect.DeepEqual(entry.DependsOn, expected.dependsOn) {
			t.Fatalf("manifest[%d].DependsOn = %#v, want %#v", index, entry.DependsOn, expected.dependsOn)
		}
		assertColumnsContain(t, entry.RequiredColumns, append([]string{"workspace_id"}, expected.identity...)...)
	}
}

func TestBuildLegacyOutboxTriggerPlanCoversEveryDMLWithImagesAndFence(t *testing.T) {
	inventory := completeLegacySchemaInventory()
	before := cloneSchemaInventoryForTest(inventory)

	plan, err := BuildLegacyOutboxTriggerPlan(inventory)
	if err != nil {
		t.Fatalf("BuildLegacyOutboxTriggerPlan: %v", err)
	}
	if plan.Mode != TaskDomainSourceLegacyWorkspace {
		t.Fatalf("plan mode = %q", plan.Mode)
	}
	if !reflect.DeepEqual(inventory, before) {
		t.Fatalf("input inventory mutated\nbefore: %#v\nafter:  %#v", before, inventory)
	}

	manifest := LegacyOutboxManifest()
	if len(plan.Upserts) != len(manifest)*2 || len(plan.Deletes) != len(manifest) {
		t.Fatalf("plan operation counts = upserts %d deletes %d", len(plan.Upserts), len(plan.Deletes))
	}
	for index, entry := range manifest {
		insert := plan.Upserts[index*2]
		update := plan.Upserts[index*2+1]
		assertTriggerSpec(t, insert, entry, TriggerInsert, TriggerAfterImage)
		assertTriggerSpec(t, update, entry, TriggerUpdate, TriggerAfterImage)

		deletedEntry := manifest[len(manifest)-1-index]
		assertTriggerSpec(t, plan.Deletes[index], deletedEntry, TriggerDelete, TriggerTombstoneBeforeImage)
	}
}

func TestBuildLegacyOutboxTriggerPlanListsEveryMissingTableAndColumn(t *testing.T) {
	inventory := completeLegacySchemaInventory()
	delete(inventory.Tables, "task_recurrence_rules")
	inventory.Tables["tasks"] = removeTestColumns(inventory.Tables["tasks"], "workspace_id", "title")
	inventory.Tables["events"] = removeTestColumns(inventory.Tables["events"], "id")

	plan, err := BuildLegacyOutboxTriggerPlan(inventory)
	if err == nil {
		t.Fatal("expected schema block")
	}
	if !reflect.DeepEqual(plan, TriggerPlan{}) {
		t.Fatalf("blocked plan must be zero, got %#v", plan)
	}
	var block *TriggerPlanBlock
	if !errors.As(err, &block) {
		t.Fatalf("error type = %T, want *TriggerPlanBlock", err)
	}
	want := []TriggerPlanBlockReason{
		{Code: TriggerPlanMissingColumn, EntityKind: ReplayEntityTask, Table: "tasks", Column: "title"},
		{Code: TriggerPlanMissingColumn, EntityKind: ReplayEntityTask, Table: "tasks", Column: "workspace_id"},
		{Code: TriggerPlanMissingTable, EntityKind: ReplayEntityRule, Table: "task_recurrence_rules"},
		{Code: TriggerPlanMissingColumn, EntityKind: ReplayEntityEvent, Table: "events", Column: "id"},
	}
	if !reflect.DeepEqual(block.Reasons, want) {
		t.Fatalf("block reasons = %#v, want %#v", block.Reasons, want)
	}
	for _, fragment := range []string{"tasks.title", "tasks.workspace_id", "task_recurrence_rules", "events.id"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not list %q", err, fragment)
		}
	}
}

func TestBuildLegacyOutboxTriggerPlanFreshV2DoesNotInspectOptionalLegacyTables(t *testing.T) {
	inventory := SchemaInventory{
		Mode: TaskDomainSourceFreshV2,
		Tables: map[string][]string{
			// A fresh database may have no legacy sources. Even a partial table
			// left by an unrelated feature must not make the installer reference it.
			"events": {"unrelated_column"},
		},
	}
	before := cloneSchemaInventoryForTest(inventory)

	plan, err := BuildLegacyOutboxTriggerPlan(inventory)
	if err != nil {
		t.Fatalf("fresh-v2 plan: %v", err)
	}
	if plan.Mode != TaskDomainSourceFreshV2 || len(plan.Upserts) != 0 || len(plan.Deletes) != 0 {
		t.Fatalf("fresh-v2 plan = %#v, want explicit empty plan", plan)
	}
	if !reflect.DeepEqual(inventory, before) {
		t.Fatalf("fresh-v2 inventory mutated\nbefore: %#v\nafter: %#v", before, inventory)
	}
}

func TestLegacyOutboxManifestAndPlanAreDefensiveCopies(t *testing.T) {
	first := LegacyOutboxManifest()
	first[0].IdentityColumns[0] = "corrupt"
	first[1].RequiredColumns[0] = "corrupt"
	first[2].DependsOn[0] = ReplayEntityEvent
	second := LegacyOutboxManifest()
	if second[0].IdentityColumns[0] != "id" || second[1].RequiredColumns[0] == "corrupt" || second[2].DependsOn[0] != ReplayEntityTask {
		t.Fatalf("manifest leaked caller mutation: %#v", second)
	}

	plan, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatal(err)
	}
	plan.Upserts[0].IdentityColumns[0] = "corrupt"
	plan.Upserts[0].RequiredColumns[0] = "corrupt"
	plan.Upserts[2].DependsOn[0] = ReplayEntityEvent
	again, err := BuildLegacyOutboxTriggerPlan(completeLegacySchemaInventory())
	if err != nil {
		t.Fatal(err)
	}
	if again.Upserts[0].IdentityColumns[0] != "id" || again.Upserts[0].RequiredColumns[0] == "corrupt" || again.Upserts[2].DependsOn[0] != ReplayEntityProject {
		t.Fatalf("plan leaked caller mutation: %#v", again.Upserts[0])
	}
}

func TestBuildLegacyOutboxTriggerPlanRejectsUnknownMode(t *testing.T) {
	plan, err := BuildLegacyOutboxTriggerPlan(SchemaInventory{Mode: "mixed"})
	if err == nil {
		t.Fatal("expected invalid mode block")
	}
	if !reflect.DeepEqual(plan, TriggerPlan{}) {
		t.Fatalf("invalid mode plan = %#v", plan)
	}
	var block *TriggerPlanBlock
	if !errors.As(err, &block) || len(block.Reasons) != 1 || block.Reasons[0].Code != TriggerPlanInvalidMode {
		t.Fatalf("invalid mode error = %#v", err)
	}
}

func completeLegacySchemaInventory() SchemaInventory {
	tables := make(map[string][]string)
	for _, entry := range LegacyOutboxManifest() {
		tables[entry.Table] = append([]string(nil), entry.RequiredColumns...)
	}
	return SchemaInventory{Mode: TaskDomainSourceLegacyWorkspace, Tables: tables}
}

func assertTriggerSpec(t *testing.T, got TriggerSpec, manifest LegacySourceManifest, operation TriggerOperation, image TriggerImageRequirement) {
	t.Helper()
	if got.EntityKind != manifest.EntityKind || got.Table != manifest.Table || got.WorkspaceColumn != manifest.WorkspaceColumn || got.DependencyOrder != manifest.DependencyOrder {
		t.Fatalf("trigger identity = %#v, manifest %#v", got, manifest)
	}
	if got.Operation != operation || got.ImageRequirement != image || !got.RequiresLogicalVersionLedger || !got.RequiresWorkspaceFence {
		t.Fatalf("trigger behavior = %#v", got)
	}
	if !reflect.DeepEqual(got.IdentityColumns, manifest.IdentityColumns) || !reflect.DeepEqual(got.RequiredColumns, manifest.RequiredColumns) {
		t.Fatalf("trigger columns = %#v, manifest %#v", got, manifest)
	}
}

func assertColumnsContain(t *testing.T, columns []string, wanted ...string) {
	t.Helper()
	set := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		set[column] = struct{}{}
	}
	for _, column := range wanted {
		if _, ok := set[column]; !ok {
			t.Fatalf("columns %#v do not include %q", columns, column)
		}
	}
}

func removeTestColumns(columns []string, removed ...string) []string {
	remove := make(map[string]struct{}, len(removed))
	for _, column := range removed {
		remove[column] = struct{}{}
	}
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, ok := remove[column]; !ok {
			result = append(result, column)
		}
	}
	return result
}

func cloneSchemaInventoryForTest(inventory SchemaInventory) SchemaInventory {
	clone := SchemaInventory{Mode: inventory.Mode, Tables: make(map[string][]string, len(inventory.Tables))}
	for table, columns := range inventory.Tables {
		clone.Tables[table] = append([]string(nil), columns...)
	}
	return clone
}
