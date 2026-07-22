package taskmigration

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestPreflightRequiresEveryLegacySourceAndKeyColumn(t *testing.T) {
	t.Parallel()

	missingSource := validLegacyInventory()
	delete(missingSource.Sources, LegacySourceEvent)
	_, err := PreflightLegacyTaskDomain(missingSource)
	assertPreflightBlock(t, err, PreflightBlockMissingSource, string(LegacySourceEvent))

	missingColumn := validLegacyInventory()
	missingColumn.Sources[LegacySourceTask] = []string{"id", "project_id"}
	_, err = PreflightLegacyTaskDomain(missingColumn)
	assertPreflightBlock(t, err, PreflightBlockMissingColumn, "priority")
}

func TestPreflightMapsPersonalInboxConflictsAndOrphanTasksDeterministically(t *testing.T) {
	t.Parallel()

	inventory := validLegacyInventory()
	inventory.Projects = []LegacyProject{
		{ID: "p-new", Name: "个人", Type: LegacyProjectPersonal, CreatedAt: instant("2026-02-01T00:00:00Z")},
		{ID: "regular-inbox", Name: "收件箱", Type: LegacyProjectRegular, CreatedAt: instant("2026-01-03T00:00:00Z")},
		{ID: "personal", Name: "个人", Type: LegacyProjectPersonal, CreatedAt: instant("2026-03-01T00:00:00Z")},
		{ID: "learning", Name: "日语", Type: LegacyProjectLearning, CreatedAt: instant("2026-01-02T00:00:00Z")},
	}
	inventory.Tasks = []LegacyTask{
		{ID: "task-valid", ProjectID: "learning", Priority: 3},
		{ID: "task-missing", ProjectID: "does-not-exist", Priority: 2},
		{ID: "task-empty", Priority: 0},
	}

	result, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		t.Fatalf("PreflightLegacyTaskDomain() error = %v", err)
	}
	if result.MigrationTimezone != "Asia/Shanghai" || result.TimezoneSource != TimezoneSourceWorkspace {
		t.Fatalf("timezone decision = (%q, %q)", result.MigrationTimezone, result.TimezoneSource)
	}

	personal := projectDecision(t, result, "personal")
	if personal.SystemRole != "personal" || personal.Kind != "standard" || personal.Horizon != "short" {
		t.Fatalf("selected personal = %#v", personal)
	}
	extraPersonal := projectDecision(t, result, "p-new")
	if extraPersonal.SystemRole != "" || extraPersonal.Kind != "standard" || extraPersonal.Horizon != "short" {
		t.Fatalf("additional personal = %#v", extraPersonal)
	}
	if extraPersonal.Name != "个人（迁移-p-new）" {
		t.Fatalf("additional personal name = %q", extraPersonal.Name)
	}
	legacyInbox := projectDecision(t, result, "regular-inbox")
	if legacyInbox.Name != "收件箱（原项目）" || legacyInbox.SystemRole != "" {
		t.Fatalf("legacy inbox conflict = %#v", legacyInbox)
	}
	systemInbox := projectDecision(t, result, "system-inbox")
	if !systemInbox.Generated || systemInbox.SystemRole != "inbox" || systemInbox.Name != "收件箱" {
		t.Fatalf("generated inbox = %#v", systemInbox)
	}

	if got := taskDecision(t, result, "task-valid").TargetProjectID; got != "learning" {
		t.Fatalf("valid task target = %q", got)
	}
	for _, taskID := range []string{"task-empty", "task-missing"} {
		if got := taskDecision(t, result, taskID).TargetProjectID; got != "system-inbox" {
			t.Fatalf("orphan task %q target = %q", taskID, got)
		}
	}

	assertAuditReason(t, result.Audit, "project", "personal", "selected_system_personal_fixed_id")
	assertAuditReason(t, result.Audit, "project", "p-new", "additional_personal_converted_and_renamed")
	assertAuditReason(t, result.Audit, "project", "regular-inbox", "inbox_name_conflict_renamed")
	assertAuditReason(t, result.Audit, "project", "", "system_inbox_created")
	assertAuditReason(t, result.Audit, "task", "task-missing", "orphan_task_mapped_to_inbox")
	assertSortedAudit(t, result.Audit)
	assertSortedProjects(t, result.Projects)
	assertSortedTasks(t, result.Tasks)
}

func TestPreflightChoosesEarliestPersonalWhenFixedIDIsAbsent(t *testing.T) {
	t.Parallel()

	inventory := validLegacyInventory()
	inventory.Projects = []LegacyProject{
		{ID: "later", Name: "后创建", Type: LegacyProjectPersonal, CreatedAt: instant("2026-02-01T00:00:00Z")},
		{ID: "tie-b", Name: "同日乙", Type: LegacyProjectPersonal, CreatedAt: instant("2026-01-01T00:00:00Z")},
		{ID: "tie-a", Name: "同日甲", Type: LegacyProjectPersonal, CreatedAt: instant("2026-01-01T00:00:00Z")},
	}

	result, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		t.Fatalf("PreflightLegacyTaskDomain() error = %v", err)
	}
	if got := projectDecision(t, result, "tie-a").SystemRole; got != "personal" {
		t.Fatalf("earliest deterministic personal role = %q", got)
	}
	assertAuditReason(t, result.Audit, "project", "tie-a", "selected_system_personal_earliest")
}

func TestPreflightBlocksPriorityOutsideZeroThroughThree(t *testing.T) {
	t.Parallel()

	for _, priority := range []int{-1, 4} {
		inventory := validLegacyInventory()
		inventory.Tasks = []LegacyTask{{ID: "bad-priority", ProjectID: "personal", Priority: priority}}
		result, err := PreflightLegacyTaskDomain(inventory)
		if result.Projects != nil || result.Tasks != nil || result.Audit != nil {
			t.Fatalf("blocked preflight returned partial result: %#v", result)
		}
		assertPreflightBlock(t, err, PreflightBlockInvalidPriority, "bad-priority")
	}
}

func TestPreflightMigrationTimezonePriorityAndValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		workspace  string
		owner      string
		deployment string
		want       string
		source     TimezoneSource
	}{
		{name: "workspace", workspace: "Asia/Tokyo", owner: "Asia/Shanghai", deployment: "Europe/Paris", want: "Asia/Tokyo", source: TimezoneSourceWorkspace},
		{name: "owner", owner: "Asia/Shanghai", deployment: "Europe/Paris", want: "Asia/Shanghai", source: TimezoneSourceOwner},
		{name: "deployment", deployment: "Europe/Paris", want: "Europe/Paris", source: TimezoneSourceDeployment},
		{name: "UTC fallback", want: "UTC", source: TimezoneSourceUTC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory := validLegacyInventory()
			inventory.WorkspaceTimezone = tc.workspace
			inventory.OwnerTimezone = tc.owner
			inventory.DeploymentTimezone = tc.deployment
			result, err := PreflightLegacyTaskDomain(inventory)
			if err != nil {
				t.Fatalf("PreflightLegacyTaskDomain() error = %v", err)
			}
			if result.MigrationTimezone != tc.want || result.TimezoneSource != tc.source {
				t.Fatalf("timezone = (%q, %q), want (%q, %q)", result.MigrationTimezone, result.TimezoneSource, tc.want, tc.source)
			}
		})
	}

	inventory := validLegacyInventory()
	inventory.WorkspaceTimezone = "Mars/Olympus_Mons"
	inventory.OwnerTimezone = "Asia/Shanghai"
	_, err := PreflightLegacyTaskDomain(inventory)
	assertPreflightBlock(t, err, PreflightBlockInvalidTimezone, "Mars/Olympus_Mons")
}

func TestPreflightValidatesAllDayEventsAtMigrationTimezoneDayBoundaries(t *testing.T) {
	t.Parallel()

	valid := validLegacyInventory()
	valid.WorkspaceTimezone = "Asia/Shanghai"
	valid.Events = []LegacyEvent{{
		ID: "valid-all-day", AllDay: true,
		StartAt: instant("2026-07-20T16:00:00Z"),
		EndAt:   instant("2026-07-23T16:00:00Z"),
	}}
	if _, err := PreflightLegacyTaskDomain(valid); err != nil {
		t.Fatalf("valid all-day preflight error = %v", err)
	}

	for _, tc := range []struct {
		name  string
		start string
		end   string
		code  PreflightBlockCode
	}{
		{name: "start not local midnight", start: "2026-07-20T17:00:00Z", end: "2026-07-21T16:00:00Z", code: PreflightBlockAllDayBoundary},
		{name: "end not local midnight", start: "2026-07-20T16:00:00Z", end: "2026-07-21T17:00:00Z", code: PreflightBlockAllDayBoundary},
		{name: "end equals start", start: "2026-07-20T16:00:00Z", end: "2026-07-20T16:00:00Z", code: PreflightBlockAllDayRange},
		{name: "end before start", start: "2026-07-21T16:00:00Z", end: "2026-07-20T16:00:00Z", code: PreflightBlockAllDayRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inventory := validLegacyInventory()
			inventory.Events = []LegacyEvent{{ID: "bad-all-day", AllDay: true, StartAt: instant(tc.start), EndAt: instant(tc.end)}}
			_, err := PreflightLegacyTaskDomain(inventory)
			assertPreflightBlock(t, err, tc.code, "bad-all-day")
		})
	}
}

func TestPreflightResultAndAuditAreIndependentOfInputOrdering(t *testing.T) {
	t.Parallel()

	first := validLegacyInventory()
	first.Projects = []LegacyProject{
		{ID: "z-personal", Name: "个人", Type: LegacyProjectPersonal, CreatedAt: instant("2026-02-01T00:00:00Z")},
		{ID: "personal", Name: "个人", Type: LegacyProjectPersonal, CreatedAt: instant("2026-03-01T00:00:00Z")},
		{ID: "a-inbox", Name: "收件箱", Type: LegacyProjectRegular, CreatedAt: instant("2026-01-01T00:00:00Z")},
	}
	first.Tasks = []LegacyTask{
		{ID: "z-task", ProjectID: "missing", Priority: 1},
		{ID: "a-task", ProjectID: "personal", Priority: 2},
	}
	second := first
	second.Projects = reverseProjects(first.Projects)
	second.Tasks = reverseTasks(first.Tasks)
	for kind, columns := range first.Sources {
		second.Sources[kind] = reverseStrings(columns)
	}

	firstResult, err := PreflightLegacyTaskDomain(first)
	if err != nil {
		t.Fatalf("first preflight error = %v", err)
	}
	secondResult, err := PreflightLegacyTaskDomain(second)
	if err != nil {
		t.Fatalf("second preflight error = %v", err)
	}
	if !reflect.DeepEqual(firstResult, secondResult) {
		t.Fatalf("preflight depends on input order:\nfirst=%#v\nsecond=%#v", firstResult, secondResult)
	}
}

func validLegacyInventory() LegacyTaskDomainInventory {
	return LegacyTaskDomainInventory{
		WorkspaceID:       "workspace-1",
		WorkspaceTimezone: "Asia/Shanghai",
		Sources: map[LegacySourceKind][]string{
			LegacySourceProject:    {"id", "name", "type", "created_at"},
			LegacySourceTask:       {"id", "project_id", "priority"},
			LegacySourceRule:       {"id", "task_id"},
			LegacySourceOccurrence: {"task_id", "occurrence_date", "status"},
			LegacySourceEvent:      {"id", "project_id", "start_time", "end_time", "is_all_day"},
			LegacySourceRoadmap:    {"id", "project_id"},
		},
		Projects: []LegacyProject{{
			ID: "personal", Name: "个人", Type: LegacyProjectPersonal,
			CreatedAt: instant("2026-01-01T00:00:00Z"),
		}},
	}
}

func assertPreflightBlock(t *testing.T, err error, code PreflightBlockCode, reference string) {
	t.Helper()
	block, ok := err.(*PreflightBlock)
	if !ok {
		t.Fatalf("error = %T(%v), want *PreflightBlock", err, err)
	}
	if block.Code != code || block.Reference != reference {
		t.Fatalf("block = %#v, want code=%q reference=%q", block, code, reference)
	}
}

func projectDecision(t *testing.T, result PreflightResult, targetID string) ProjectDecision {
	t.Helper()
	for _, decision := range result.Projects {
		if decision.TargetID == targetID {
			return decision
		}
	}
	t.Fatalf("project decision %q not found", targetID)
	return ProjectDecision{}
}

func taskDecision(t *testing.T, result PreflightResult, legacyID string) TaskDecision {
	t.Helper()
	for _, decision := range result.Tasks {
		if decision.LegacyID == legacyID {
			return decision
		}
	}
	t.Fatalf("task decision %q not found", legacyID)
	return TaskDecision{}
}

func assertAuditReason(t *testing.T, audit []AuditDecision, entityKind, legacyID, reason string) {
	t.Helper()
	for _, decision := range audit {
		if decision.EntityKind == entityKind && decision.LegacyID == legacyID && decision.Reason == reason {
			return
		}
	}
	t.Fatalf("audit decision (%q, %q, %q) not found in %#v", entityKind, legacyID, reason, audit)
}

func assertSortedAudit(t *testing.T, audit []AuditDecision) {
	t.Helper()
	keys := make([]string, len(audit))
	for index, decision := range audit {
		keys[index] = decision.EntityKind + "\x00" + decision.LegacyID + "\x00" + decision.Reason + "\x00" + decision.TargetID
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("audit is not sorted: %#v", audit)
	}
}

func assertSortedProjects(t *testing.T, projects []ProjectDecision) {
	t.Helper()
	ids := make([]string, len(projects))
	for index, project := range projects {
		ids[index] = project.TargetID
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("projects are not sorted: %#v", projects)
	}
}

func assertSortedTasks(t *testing.T, tasks []TaskDecision) {
	t.Helper()
	ids := make([]string, len(tasks))
	for index, task := range tasks {
		ids[index] = task.LegacyID
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("tasks are not sorted: %#v", tasks)
	}
}

func reverseProjects(values []LegacyProject) []LegacyProject {
	result := append([]LegacyProject(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseTasks(values []LegacyTask) []LegacyTask {
	result := append([]LegacyTask(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func reverseStrings(values []string) []string {
	result := append([]string(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func instant(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
