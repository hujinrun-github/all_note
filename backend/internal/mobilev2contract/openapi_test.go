package mobilev2contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestMTDV2Contract001OpenAPIAndChecksumAreCurrent(t *testing.T) {
	contractPath := filepath.Join("..", "..", "api", "mobile-v2.openapi.yaml")
	checksumPath := filepath.Join("..", "..", "api", "mobile-v2.sha256")
	contract, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read mobile-v2 OpenAPI: %v", err)
	}
	checksum, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatalf("read mobile-v2 checksum: %v", err)
	}
	digest := sha256.Sum256(contract)
	want := hex.EncodeToString(digest[:])
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || fields[0] != want || fields[1] != "mobile-v2.openapi.yaml" {
		t.Fatalf("mobile-v2.sha256 is stale: got %q, want %s  mobile-v2.openapi.yaml", strings.TrimSpace(string(checksum)), want)
	}
}

func TestMTDV2Contract002IdentityAndOfflineOccurrenceCorrelation(t *testing.T) {
	document := loadMobileV2Document(t)
	assertSchemaProperties(t, document, "IdentityMapping", []string{"client_id", "entity_id", "entity_type"})
	assertSchemaProperties(t, document, "EntityEnvelope", []string{"aggregate_revisions", "client_id", "deleted_at", "entity_id", "entity_revision", "entity_type", "payload"})
	assertSchemaRequired(t, document, "TaskCreatePayload", []string{"initial_occurrence_client_id", "project", "task_client_id"})
	assertSchemaProperties(t, document, "CommandTarget", []string{"client_id", "entity_id"})
	if got := schemaMap(t, document, "EntityEnvelope")["x-local-primary-key"]; got != "immutable-local-id" {
		t.Fatalf("EntityEnvelope x-local-primary-key = %#v, want immutable-local-id", got)
	}
}

func TestMTDV2Contract003CommandRevisionMatrixIsExplicit(t *testing.T) {
	document := loadMobileV2Document(t)
	matrix := stringMap(t, document, "x-command-revision-matrix")
	want := map[string]string{
		"project.create":                         "none",
		"project.update":                         "project",
		"project.activate":                       "project",
		"project.pause":                          "project",
		"project.resume":                         "project",
		"project.complete":                       "project",
		"project.archive":                        "project",
		"project.restore":                        "project",
		"task.create":                            "project-or-dependency",
		"task.update":                            "task",
		"occurrence.start":                       "task,occurrence",
		"occurrence.block":                       "task,occurrence",
		"occurrence.unblock":                     "task,occurrence",
		"occurrence.complete":                    "task,occurrence",
		"occurrence.skip":                        "task,occurrence",
		"occurrence.cancel":                      "task,occurrence",
		"occurrence.reopen":                      "task,occurrence",
		"occurrence.reschedule-only-this":        "task,occurrence",
		"schedule.reschedule-this-and-following": "task,schedule,occurrence",
	}
	for command, expected := range want {
		if got := matrix[command]; got != expected {
			t.Errorf("revision matrix[%s] = %q, want %q", command, got, expected)
		}
	}
	assertSchemaRequired(t, document, "ExpectedRevision", []string{"source"})
	assertSchemaEnum(t, document, "ExpectedRevisionSource", []string{"exact", "from_dependency_receipt"})
}

func TestMTDV2Contract005CutoverRequiresUpgradeInsteadOfV1Fallback(t *testing.T) {
	document := loadMobileV2Document(t)
	cutover := mapValue(t, document, "x-mobile-v1-cutover")
	if cutover["http-status"] != uint64(426) && cutover["http-status"] != 426 {
		t.Fatalf("cutover http-status = %#v, want 426", cutover["http-status"])
	}
	if cutover["error-code"] != "upgrade_required" {
		t.Fatalf("cutover error-code = %#v, want upgrade_required", cutover["error-code"])
	}
	assertSchemaEnum(t, document, "WorkspaceMode", []string{"legacy-active", "upgrade-required", "v2-active", "v2-cutover-migrating", "v2-shadow-readonly"})
}

func TestMTDV2Contract013EntityFieldMatrixIsComplete(t *testing.T) {
	document := loadMobileV2Document(t)
	assertSchemaEnum(t, document, "ProjectKind", []string{"learning", "standard"})
	assertSchemaEnum(t, document, "ProjectHorizon", []string{"long", "short"})
	assertSchemaEnum(t, document, "ProjectSystemRole", []string{"inbox", "personal"})
	assertSchemaEnum(t, document, "TaskLifecycleStatus", []string{"active", "archived", "cancelled", "completed", "draft", "paused"})
	assertSchemaEnum(t, document, "GenerationStatus", []string{"failed", "idle", "retry_pending", "running"})
	assertSchemaEnum(t, document, "RecurrenceType", []string{"daily", "monthly", "none", "weekly"})
	assertSchemaEnum(t, document, "TimingType", []string{"date", "time_block", "unscheduled"})
	assertSchemaEnum(t, document, "ExecutionStatus", []string{"active", "blocked", "cancelled", "done", "open", "skipped"})
	assertSchemaEnum(t, document, "RoadmapStatus", []string{"active", "archived", "completed", "draft", "failed"})
	assertSchemaEnum(t, document, "RoadmapNodeType", []string{"milestone", "stage", "topic"})
	assertSchemaEnum(t, document, "RoadmapNodeStatus", []string{"available", "in_progress", "locked", "mastered", "skipped"})
	assertSchemaEnum(t, document, "RoadmapEdgeType", []string{"prerequisite", "related", "suggested_order"})
	want := map[string][]string{
		"ProjectPayload":             {"archived_at", "archived_from_status", "created_at", "description", "horizon", "kind", "name", "status", "system_role", "target_at", "updated_at"},
		"TaskPayload":                {"archived_at", "created_at", "description", "lifecycle_status", "note_id", "priority", "project_id", "roadmap_node_id", "sort_order", "title", "updated_at"},
		"TaskSchedulePayload":        {"current_schedule_revision", "generation_error", "generation_retry_at", "generation_status", "generation_watermark", "task_id", "updated_at"},
		"ScheduleVersionPayload":     {"created_at", "duration_minutes", "effective_from", "effective_to", "ends_on", "local_start_time", "recurrence_type", "rule", "schedule_revision", "starts_on", "task_id", "timezone", "timing_type"},
		"TaskOccurrencePayload":      {"actual_start_at", "all_day_end_date", "blocked_reason", "calendar_kind", "calendar_notes", "completed_at", "created_at", "due_at", "execution_status", "generated_schedule_revision", "location", "next_action", "note_id", "occurrence_key", "override_description", "override_title", "planned_date", "planned_end_at", "planned_start_at", "task_id", "updated_at"},
		"LearningRoadmapPayload":     {"created_at", "description", "project_id", "status", "title", "updated_at"},
		"RoadmapNodePayload":         {"created_at", "description", "legacy_metadata", "node_type", "parent_id", "position", "project_id", "roadmap_id", "status", "title", "updated_at"},
		"RoadmapEdgePayload":         {"created_at", "edge_type", "from_node_id", "project_id", "roadmap_id", "to_node_id"},
		"RoadmapNodeProgressPayload": {"active", "as_of_sequence", "blocked", "cancelled", "done", "open", "roadmap_node_id", "skipped", "total"},
	}
	for name, fields := range want {
		assertSchemaProperties(t, document, name, fields)
		assertSchemaRequired(t, document, name, fields)
	}
	assertSchemaEnum(t, document, "EntityType", []string{"learning_roadmap", "project", "roadmap_edge", "roadmap_node", "roadmap_node_progress", "schedule_version", "task", "task_occurrence", "task_schedule"})
}

func TestMTDV2Contract014ProjectLifecycleUsesDedicatedCommands(t *testing.T) {
	document := loadMobileV2Document(t)
	commands := schemaEnum(t, document, "CommandType")
	for _, command := range []string{"project.create", "project.update", "project.activate", "project.pause", "project.resume", "project.complete", "project.archive", "project.restore"} {
		if !contains(commands, command) {
			t.Errorf("CommandType missing %s", command)
		}
	}
	if contains(commands, "project.delete") {
		t.Error("CommandType must not expose project.delete in mobile-v2")
	}
	properties := propertyNames(t, schemaMap(t, document, "ProjectUpdatePayload"))
	for _, forbidden := range []string{"archived_from_status", "status", "system_role"} {
		if contains(properties, forbidden) {
			t.Errorf("ProjectUpdatePayload must not expose lifecycle field %s", forbidden)
		}
	}
	assertSchemaEnum(t, document, "ProjectStatus", []string{"active", "archived", "completed", "paused", "planning"})
}

func TestMTDV2Contract015ConflictsAreClientLocalOnly(t *testing.T) {
	document := loadMobileV2Document(t)
	paths := mapValue(t, document, "paths")
	for path := range paths {
		if strings.Contains(path, "conflict") {
			t.Errorf("mobile-v2 must not expose server conflict resource: %s", path)
		}
	}
	if _, ok := mapValue(t, document, "components")["schemas"].(map[string]any)["ConflictResolution"]; ok {
		t.Error("mobile-v2 must not define a ConflictResolution schema")
	}
}

func TestMTDV2Contract017WorkspaceModesAndScopesAreFrozen(t *testing.T) {
	document := loadMobileV2Document(t)
	assertSchemaEnum(t, document, "WorkspaceMode", []string{"legacy-active", "upgrade-required", "v2-active", "v2-cutover-migrating", "v2-shadow-readonly"})
	assertSchemaEnum(t, document, "SyncScopeName", []string{"iphone-content", "iphone-occurrence-window", "iphone-task-core", "watch-occurrence-window"})
	assertPropertyEnum(t, document, "APIError", "code", []string{"lifecycle_command_required", "projection_refresh_required", "receipt_history_ambiguous", "resync_required", "restore_target_required", "stale_runtime_epoch", "upgrade_required", "workspace_gone", "workspace_mode_forbids_command"})
	assertSchemaProperties(t, document, "MobileV2Capabilities", []string{"contract_sha256", "features", "minimum_client_build", "mobile_contract_epoch", "runtime_epoch", "schema_version", "server_cutover_epoch", "sync_scopes", "task_model_version", "workspace_id", "workspace_mode"})
}

func loadMobileV2Document(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "mobile-v2.openapi.yaml"))
	if err != nil {
		t.Fatalf("read mobile-v2 OpenAPI: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode mobile-v2 OpenAPI: %v", err)
	}
	return document
}

func schemaMap(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	components := mapValue(t, document, "components")
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("components.schemas has type %T", components["schemas"])
	}
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("missing schema %s", name)
	}
	return schema
}

func mapValue(t *testing.T, values map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := values[key].(map[string]any)
	if !ok {
		t.Fatalf("%s has type %T, want object", key, values[key])
	}
	return value
}

func stringMap(t *testing.T, values map[string]any, key string) map[string]string {
	t.Helper()
	object := mapValue(t, values, key)
	result := make(map[string]string, len(object))
	for name, value := range object {
		result[name] = fmt.Sprint(value)
	}
	return result
}

func propertyNames(t *testing.T, schema map[string]any) []string {
	t.Helper()
	properties := mapValue(t, schema, "properties")
	result := make([]string, 0, len(properties))
	for name := range properties {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func stringSlice(t *testing.T, value any, label string) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s has type %T, want array", label, value)
	}
	result := make([]string, len(items))
	for index, item := range items {
		result[index] = fmt.Sprint(item)
	}
	sort.Strings(result)
	return result
}

func schemaEnum(t *testing.T, document map[string]any, name string) []string {
	t.Helper()
	return stringSlice(t, schemaMap(t, document, name)["enum"], name+".enum")
}

func assertSchemaProperties(t *testing.T, document map[string]any, name string, want []string) {
	t.Helper()
	got := propertyNames(t, schemaMap(t, document, name))
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s properties = %v, want %v", name, got, want)
	}
}

func assertSchemaRequired(t *testing.T, document map[string]any, name string, want []string) {
	t.Helper()
	got := stringSlice(t, schemaMap(t, document, name)["required"], name+".required")
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s required = %v, want %v", name, got, want)
	}
}

func assertSchemaEnum(t *testing.T, document map[string]any, name string, want []string) {
	t.Helper()
	got := schemaEnum(t, document, name)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s enum = %v, want %v", name, got, want)
	}
}

func assertPropertyEnum(t *testing.T, document map[string]any, schemaName, propertyName string, want []string) {
	t.Helper()
	properties := mapValue(t, schemaMap(t, document, schemaName), "properties")
	property, ok := properties[propertyName].(map[string]any)
	if !ok {
		t.Fatalf("%s.%s has type %T, want object", schemaName, propertyName, properties[propertyName])
	}
	got := stringSlice(t, property["enum"], schemaName+"."+propertyName+".enum")
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s.%s enum = %v, want %v", schemaName, propertyName, got, want)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
