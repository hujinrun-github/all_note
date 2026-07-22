package taskmigration

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type LegacySourceKind string

const (
	LegacySourceProject    LegacySourceKind = "project"
	LegacySourceTask       LegacySourceKind = "task"
	LegacySourceRule       LegacySourceKind = "rule"
	LegacySourceOccurrence LegacySourceKind = "occurrence"
	LegacySourceEvent      LegacySourceKind = "event"
	LegacySourceRoadmap    LegacySourceKind = "roadmap"
)

type LegacyProjectType string

const (
	LegacyProjectPersonal LegacyProjectType = "personal"
	LegacyProjectRegular  LegacyProjectType = "regular"
	LegacyProjectLearning LegacyProjectType = "learning"
)

type TimezoneSource string

const (
	TimezoneSourceWorkspace  TimezoneSource = "workspace"
	TimezoneSourceOwner      TimezoneSource = "owner"
	TimezoneSourceDeployment TimezoneSource = "deployment"
	TimezoneSourceUTC        TimezoneSource = "utc"
)

type PreflightBlockCode string

const (
	PreflightBlockMissingSource   PreflightBlockCode = "missing_source"
	PreflightBlockMissingColumn   PreflightBlockCode = "missing_column"
	PreflightBlockInvalidPriority PreflightBlockCode = "invalid_priority"
	PreflightBlockInvalidTimezone PreflightBlockCode = "invalid_timezone"
	PreflightBlockAllDayBoundary  PreflightBlockCode = "invalid_all_day_boundary"
	PreflightBlockAllDayRange     PreflightBlockCode = "invalid_all_day_range"
	PreflightBlockReservedID      PreflightBlockCode = "reserved_project_id"
)

// PreflightBlock is deterministic and safe to persist as a migration audit
// failure. Reference identifies the source kind, column, timezone, or entity
// that requires operator action.
type PreflightBlock struct {
	Code      PreflightBlockCode
	Reference string
	Detail    string
}

func (e *PreflightBlock) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reference)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Reference, e.Detail)
}

type LegacyProject struct {
	ID        string
	Name      string
	Type      LegacyProjectType
	CreatedAt time.Time
}

type LegacyTask struct {
	ID        string
	ProjectID string
	Priority  int
}

type LegacyEvent struct {
	ID      string
	AllDay  bool
	StartAt time.Time
	EndAt   time.Time
}

// LegacyTaskDomainInventory is a read-only snapshot supplied by a later
// provider-specific inventory reader. This package never opens a database.
type LegacyTaskDomainInventory struct {
	WorkspaceID        string
	Sources            map[LegacySourceKind][]string
	Projects           []LegacyProject
	Tasks              []LegacyTask
	Events             []LegacyEvent
	WorkspaceTimezone  string
	OwnerTimezone      string
	DeploymentTimezone string
}

type ProjectDecision struct {
	LegacyID   string
	TargetID   string
	Name       string
	Kind       string
	Horizon    string
	SystemRole string
	Generated  bool
}

type TaskDecision struct {
	LegacyID        string
	TargetProjectID string
}

// AuditDecision records every generated identity or non-identity mapping.
// The result is sorted before return so repeated preflight runs serialize
// identically.
type AuditDecision struct {
	EntityKind string
	LegacyID   string
	TargetID   string
	Reason     string
}

type PreflightResult struct {
	MigrationTimezone string
	TimezoneSource    TimezoneSource
	Projects          []ProjectDecision
	Tasks             []TaskDecision
	Audit             []AuditDecision
}

var requiredSourceColumns = []struct {
	kind    LegacySourceKind
	columns []string
}{
	{kind: LegacySourceProject, columns: []string{"id", "name", "type", "created_at"}},
	{kind: LegacySourceTask, columns: []string{"id", "project_id", "priority"}},
	{kind: LegacySourceRule, columns: []string{"id", "task_id"}},
	{kind: LegacySourceOccurrence, columns: []string{"task_id", "occurrence_date", "status"}},
	{kind: LegacySourceEvent, columns: []string{"id", "project_id", "start_time", "end_time", "is_all_day"}},
	{kind: LegacySourceRoadmap, columns: []string{"id", "project_id"}},
}

// PreflightLegacyTaskDomain performs only deterministic validation and mapping
// decisions. It does not initialize schema, read providers, or start a
// migration.
func PreflightLegacyTaskDomain(inventory LegacyTaskDomainInventory) (PreflightResult, error) {
	if err := validateLegacySources(inventory.Sources); err != nil {
		return PreflightResult{}, err
	}
	timezone, timezoneSource, err := chooseMigrationTimezone(inventory)
	if err != nil {
		return PreflightResult{}, err
	}
	if err := validateLegacyTasks(inventory.Tasks); err != nil {
		return PreflightResult{}, err
	}
	if err := validateLegacyEvents(inventory.Events, timezone); err != nil {
		return PreflightResult{}, err
	}

	projects, projectIDs, projectAudit, err := mapLegacyProjects(inventory.Projects)
	if err != nil {
		return PreflightResult{}, err
	}
	tasks, taskAudit := mapLegacyTasks(inventory.Tasks, projectIDs)
	audit := make([]AuditDecision, 0, 1+len(projectAudit)+len(taskAudit))
	audit = append(audit, AuditDecision{
		EntityKind: "workspace",
		TargetID:   inventory.WorkspaceID,
		Reason:     "migration_timezone_" + string(timezoneSource),
	})
	audit = append(audit, projectAudit...)
	audit = append(audit, taskAudit...)
	sortAuditDecisions(audit)

	return PreflightResult{
		MigrationTimezone: timezone,
		TimezoneSource:    timezoneSource,
		Projects:          projects,
		Tasks:             tasks,
		Audit:             audit,
	}, nil
}

func validateLegacySources(sources map[LegacySourceKind][]string) error {
	for _, required := range requiredSourceColumns {
		columns, ok := sources[required.kind]
		if !ok {
			return &PreflightBlock{Code: PreflightBlockMissingSource, Reference: string(required.kind)}
		}
		available := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			available[strings.TrimSpace(column)] = struct{}{}
		}
		for _, column := range required.columns {
			if _, ok := available[column]; !ok {
				return &PreflightBlock{
					Code: PreflightBlockMissingColumn, Reference: column,
					Detail: "source=" + string(required.kind),
				}
			}
		}
	}
	return nil
}

func chooseMigrationTimezone(inventory LegacyTaskDomainInventory) (string, TimezoneSource, error) {
	candidates := []struct {
		value  string
		source TimezoneSource
	}{
		{value: inventory.WorkspaceTimezone, source: TimezoneSourceWorkspace},
		{value: inventory.OwnerTimezone, source: TimezoneSourceOwner},
		{value: inventory.DeploymentTimezone, source: TimezoneSourceDeployment},
		{value: "UTC", source: TimezoneSourceUTC},
	}
	for _, candidate := range candidates {
		value := strings.TrimSpace(candidate.value)
		if value == "" {
			continue
		}
		if _, err := time.LoadLocation(value); err != nil {
			return "", "", &PreflightBlock{
				Code: PreflightBlockInvalidTimezone, Reference: value,
				Detail: "source=" + string(candidate.source),
			}
		}
		return value, candidate.source, nil
	}
	panic("UTC timezone candidate must always be present")
}

func validateLegacyTasks(tasks []LegacyTask) error {
	ordered := append([]LegacyTask(nil), tasks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, task := range ordered {
		if task.Priority < 0 || task.Priority > 3 {
			return &PreflightBlock{
				Code: PreflightBlockInvalidPriority, Reference: task.ID,
				Detail: fmt.Sprintf("priority=%d", task.Priority),
			}
		}
	}
	return nil
}

func validateLegacyEvents(events []LegacyEvent, timezone string) error {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return &PreflightBlock{Code: PreflightBlockInvalidTimezone, Reference: timezone}
	}
	ordered := append([]LegacyEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, event := range ordered {
		if !event.AllDay {
			continue
		}
		if !event.EndAt.After(event.StartAt) {
			return &PreflightBlock{Code: PreflightBlockAllDayRange, Reference: event.ID}
		}
		if !isLocalMidnight(event.StartAt, location) || !isLocalMidnight(event.EndAt, location) {
			return &PreflightBlock{Code: PreflightBlockAllDayBoundary, Reference: event.ID, Detail: "timezone=" + timezone}
		}
	}
	return nil
}

func isLocalMidnight(instant time.Time, location *time.Location) bool {
	local := instant.In(location)
	return local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0
}

func mapLegacyProjects(projects []LegacyProject) ([]ProjectDecision, map[string]struct{}, []AuditDecision, error) {
	ordered := append([]LegacyProject(nil), projects...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for _, project := range ordered {
		if project.ID == "system-inbox" {
			return nil, nil, nil, &PreflightBlock{Code: PreflightBlockReservedID, Reference: project.ID}
		}
	}

	selectedPersonal, selectionReason := selectSystemPersonal(ordered)
	nameCounts := make(map[string]int, len(ordered))
	for _, project := range ordered {
		nameCounts[project.Name]++
	}
	decisions := make([]ProjectDecision, 0, len(ordered)+2)
	audit := make([]AuditDecision, 0, len(ordered)+2)
	projectIDs := make(map[string]struct{}, len(ordered)+2)

	for _, project := range ordered {
		decision := ProjectDecision{
			LegacyID: project.ID, TargetID: project.ID, Name: project.Name,
		}
		switch project.Type {
		case LegacyProjectPersonal:
			decision.Kind = "standard"
			decision.Horizon = "short"
			if project.ID == selectedPersonal {
				decision.SystemRole = "personal"
				audit = append(audit, AuditDecision{
					EntityKind: "project", LegacyID: project.ID, TargetID: project.ID, Reason: selectionReason,
				})
			} else {
				reason := "additional_personal_converted"
				if nameCounts[project.Name] > 1 {
					decision.Name = "个人（迁移-" + shortLegacyID(project.ID) + "）"
					reason = "additional_personal_converted_and_renamed"
				}
				audit = append(audit, AuditDecision{
					EntityKind: "project", LegacyID: project.ID, TargetID: project.ID, Reason: reason,
				})
			}
		case LegacyProjectLearning:
			decision.Kind = "learning"
			decision.Horizon = "long"
		default:
			decision.Kind = "standard"
			decision.Horizon = "long"
		}
		if decision.SystemRole == "" && decision.Name == "收件箱" {
			decision.Name = "收件箱（原项目）"
			audit = append(audit, AuditDecision{
				EntityKind: "project", LegacyID: project.ID, TargetID: project.ID, Reason: "inbox_name_conflict_renamed",
			})
		}
		decisions = append(decisions, decision)
		projectIDs[decision.TargetID] = struct{}{}
	}

	if selectedPersonal == "" {
		if _, occupied := projectIDs["personal"]; occupied {
			return nil, nil, nil, &PreflightBlock{Code: PreflightBlockReservedID, Reference: "personal"}
		}
		decisions = append(decisions, ProjectDecision{
			TargetID: "personal", Name: "个人事务", Kind: "standard", Horizon: "short", SystemRole: "personal", Generated: true,
		})
		projectIDs["personal"] = struct{}{}
		audit = append(audit, AuditDecision{EntityKind: "project", TargetID: "personal", Reason: "system_personal_created"})
	}

	decisions = append(decisions, ProjectDecision{
		TargetID: "system-inbox", Name: "收件箱", Kind: "standard", Horizon: "short", SystemRole: "inbox", Generated: true,
	})
	projectIDs["system-inbox"] = struct{}{}
	audit = append(audit, AuditDecision{EntityKind: "project", TargetID: "system-inbox", Reason: "system_inbox_created"})

	sort.Slice(decisions, func(i, j int) bool { return decisions[i].TargetID < decisions[j].TargetID })
	return decisions, projectIDs, audit, nil
}

func selectSystemPersonal(projects []LegacyProject) (string, string) {
	personal := make([]LegacyProject, 0)
	for _, project := range projects {
		if project.Type != LegacyProjectPersonal {
			continue
		}
		if project.ID == "personal" {
			return project.ID, "selected_system_personal_fixed_id"
		}
		personal = append(personal, project)
	}
	if len(personal) == 0 {
		return "", ""
	}
	sort.Slice(personal, func(i, j int) bool {
		if personal[i].CreatedAt.Equal(personal[j].CreatedAt) {
			return personal[i].ID < personal[j].ID
		}
		return personal[i].CreatedAt.Before(personal[j].CreatedAt)
	})
	return personal[0].ID, "selected_system_personal_earliest"
}

func mapLegacyTasks(tasks []LegacyTask, projectIDs map[string]struct{}) ([]TaskDecision, []AuditDecision) {
	ordered := append([]LegacyTask(nil), tasks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	decisions := make([]TaskDecision, 0, len(ordered))
	audit := make([]AuditDecision, 0)
	for _, task := range ordered {
		targetProjectID := task.ProjectID
		if _, exists := projectIDs[targetProjectID]; targetProjectID == "" || !exists {
			targetProjectID = "system-inbox"
			audit = append(audit, AuditDecision{
				EntityKind: "task", LegacyID: task.ID, TargetID: task.ID, Reason: "orphan_task_mapped_to_inbox",
			})
		}
		decisions = append(decisions, TaskDecision{LegacyID: task.ID, TargetProjectID: targetProjectID})
	}
	return decisions, audit
}

func shortLegacyID(id string) string {
	runes := []rune(id)
	if len(runes) > 8 {
		runes = runes[:8]
	}
	return string(runes)
}

func sortAuditDecisions(audit []AuditDecision) {
	sort.Slice(audit, func(i, j int) bool {
		left := audit[i]
		right := audit[j]
		if left.EntityKind != right.EntityKind {
			return left.EntityKind < right.EntityKind
		}
		if left.LegacyID != right.LegacyID {
			return left.LegacyID < right.LegacyID
		}
		if left.Reason != right.Reason {
			return left.Reason < right.Reason
		}
		return left.TargetID < right.TargetID
	})
}
