package taskmigration

import (
	"fmt"
	"sort"
	"strings"
)

// TaskDomainSourceMode tells the trigger installer whether legacy tables are
// the current write model. Fresh v2 tenants deliberately have no legacy
// trigger plan: their optional legacy tables must never be referenced merely
// because another deployment happens to have them.
type TaskDomainSourceMode string

const (
	TaskDomainSourceLegacyWorkspace TaskDomainSourceMode = "legacy_workspace"
	TaskDomainSourceFreshV2         TaskDomainSourceMode = "fresh_v2"
)

// SchemaInventory is the provider-neutral view used during trigger planning.
// Provider adapters are responsible for exposing canonical legacy column
// names (for example, mapping a dialect-specific timestamp spelling before
// calling this package). BuildLegacyOutboxTriggerPlan never mutates it.
type SchemaInventory struct {
	Mode   TaskDomainSourceMode
	Tables map[string][]string
}

// LegacySourceManifest describes one complete normalized legacy source image.
// DependencyOrder is the order in which upserts may be projected. Deletes use
// the exact reverse order. RequiredColumns are sorted to make audit output and
// generated installer plans stable across processes.
type LegacySourceManifest struct {
	EntityKind      ReplayEntityKind
	Table           string
	WorkspaceColumn string
	IdentityColumns []string
	DependsOn       []ReplayEntityKind
	DependencyOrder int
	RequiredColumns []string
}

var legacyOutboxManifest = []LegacySourceManifest{
	{
		EntityKind:      ReplayEntityProject,
		Table:           "task_projects",
		WorkspaceColumn: "workspace_id",
		IdentityColumns: []string{"id"},
		DependencyOrder: 0,
		RequiredColumns: []string{"id", "name", "type", "workspace_id"},
	},
	{
		EntityKind:      ReplayEntityTask,
		Table:           "tasks",
		WorkspaceColumn: "workspace_id",
		IdentityColumns: []string{"id"},
		DependsOn:       []ReplayEntityKind{ReplayEntityProject},
		DependencyOrder: 1,
		RequiredColumns: []string{
			"completed_at", "content", "done", "due_at", "execution_type",
			"horizon", "id", "note_id", "planned_date", "priority", "project_id",
			"roadmap_node_id", "scope", "sort_order", "status", "title", "updated_at",
			"workspace_id",
		},
	},
	{
		EntityKind:      ReplayEntityRule,
		Table:           "task_recurrence_rules",
		WorkspaceColumn: "workspace_id",
		IdentityColumns: []string{"task_id"},
		DependsOn:       []ReplayEntityKind{ReplayEntityTask},
		DependencyOrder: 2,
		RequiredColumns: []string{
			"enabled", "end_date", "frequency", "interval", "month_days", "start_date",
			"task_id", "timezone", "updated_at", "weekdays", "workspace_id",
		},
	},
	{
		EntityKind:      ReplayEntityOccurrence,
		Table:           "task_occurrences",
		WorkspaceColumn: "workspace_id",
		IdentityColumns: []string{"task_id", "occurrence_date"},
		DependsOn:       []ReplayEntityKind{ReplayEntityTask, ReplayEntityRule},
		DependencyOrder: 3,
		RequiredColumns: []string{
			"completed_at", "note", "occurrence_date", "status", "task_id", "updated_at", "workspace_id",
		},
	},
	{
		EntityKind:      ReplayEntityEvent,
		Table:           "events",
		WorkspaceColumn: "workspace_id",
		IdentityColumns: []string{"id"},
		DependsOn:       []ReplayEntityKind{ReplayEntityProject},
		DependencyOrder: 4,
		RequiredColumns: []string{
			"end_time", "id", "is_all_day", "kind", "location", "note_id", "notes",
			"project_id", "start_time", "title", "updated_at", "workspace_id",
		},
	},
}

// LegacyOutboxManifest returns a deep copy so provider-specific planning code
// cannot change the canonical dependency or identity contract.
func LegacyOutboxManifest() []LegacySourceManifest {
	return cloneLegacyManifest(legacyOutboxManifest)
}

type TriggerOperation string

const (
	TriggerInsert TriggerOperation = "INSERT"
	TriggerUpdate TriggerOperation = "UPDATE"
	TriggerDelete TriggerOperation = "DELETE"
)

type TriggerImageRequirement string

const (
	// TriggerAfterImage means NEW must be sufficient to replay the upsert
	// without rereading the source table.
	TriggerAfterImage TriggerImageRequirement = "normalized_after_image"
	// TriggerTombstoneBeforeImage means OLD must be sufficient to project the
	// deletion after the source row (including a cascade) no longer exists.
	TriggerTombstoneBeforeImage TriggerImageRequirement = "tombstone_before_image"
)

// TriggerSpec is intentionally SQL-free. Dialect installers render it while
// preserving the image, identity, logical-version ledger, and fence contract.
type TriggerSpec struct {
	EntityKind                   ReplayEntityKind
	Table                        string
	WorkspaceColumn              string
	IdentityColumns              []string
	DependsOn                    []ReplayEntityKind
	DependencyOrder              int
	RequiredColumns              []string
	Operation                    TriggerOperation
	ImageRequirement             TriggerImageRequirement
	RequiresLogicalVersionLedger bool
	RequiresWorkspaceFence       bool
}

type TriggerPlan struct {
	Mode          TaskDomainSourceMode
	TaskDueColumn string
	Upserts       []TriggerSpec
	Deletes       []TriggerSpec
}

type TriggerPlanBlockCode string

const (
	TriggerPlanInvalidMode   TriggerPlanBlockCode = "invalid_source_mode"
	TriggerPlanMissingTable  TriggerPlanBlockCode = "missing_legacy_table"
	TriggerPlanMissingColumn TriggerPlanBlockCode = "missing_legacy_column"
)

type TriggerPlanBlockReason struct {
	Code       TriggerPlanBlockCode
	EntityKind ReplayEntityKind
	Table      string
	Column     string
}

// TriggerPlanBlock lists every schema defect in deterministic manifest/column
// order so operators can repair one tenant schema in a single pass.
type TriggerPlanBlock struct {
	Reasons []TriggerPlanBlockReason
}

func (e *TriggerPlanBlock) Error() string {
	if e == nil || len(e.Reasons) == 0 {
		return "legacy outbox trigger plan blocked"
	}
	parts := make([]string, 0, len(e.Reasons))
	for _, reason := range e.Reasons {
		switch reason.Code {
		case TriggerPlanInvalidMode:
			parts = append(parts, fmt.Sprintf("%s:%s", reason.Code, reason.Column))
		case TriggerPlanMissingTable:
			parts = append(parts, fmt.Sprintf("%s:%s", reason.Code, reason.Table))
		default:
			parts = append(parts, fmt.Sprintf("%s:%s.%s", reason.Code, reason.Table, reason.Column))
		}
	}
	return "legacy outbox trigger plan blocked: " + strings.Join(parts, ", ")
}

// BuildLegacyOutboxTriggerPlan validates the complete legacy schema before it
// returns any work. A blocked result is always a zero plan, preventing callers
// from installing only a subset of the required transactional triggers.
func BuildLegacyOutboxTriggerPlan(inventory SchemaInventory) (TriggerPlan, error) {
	switch inventory.Mode {
	case TaskDomainSourceFreshV2:
		return TriggerPlan{
			Mode:    TaskDomainSourceFreshV2,
			Upserts: []TriggerSpec{},
			Deletes: []TriggerSpec{},
		}, nil
	case TaskDomainSourceLegacyWorkspace:
		// Continue with complete schema validation below.
	default:
		return TriggerPlan{}, &TriggerPlanBlock{Reasons: []TriggerPlanBlockReason{{
			Code:   TriggerPlanInvalidMode,
			Column: string(inventory.Mode),
		}}}
	}

	manifest := LegacyOutboxManifest()
	reasons := validateLegacyTriggerInventory(inventory, manifest)
	if len(reasons) != 0 {
		return TriggerPlan{}, &TriggerPlanBlock{Reasons: reasons}
	}

	plan := TriggerPlan{
		Mode:          TaskDomainSourceLegacyWorkspace,
		TaskDueColumn: legacyTaskDuePhysicalColumn(inventory.Tables["tasks"]),
		Upserts:       make([]TriggerSpec, 0, len(manifest)*2),
		Deletes:       make([]TriggerSpec, 0, len(manifest)),
	}
	for _, entry := range manifest {
		plan.Upserts = append(plan.Upserts,
			triggerSpec(entry, TriggerInsert, TriggerAfterImage),
			triggerSpec(entry, TriggerUpdate, TriggerAfterImage),
		)
	}
	for index := len(manifest) - 1; index >= 0; index-- {
		plan.Deletes = append(plan.Deletes, triggerSpec(manifest[index], TriggerDelete, TriggerTombstoneBeforeImage))
	}
	return plan, nil
}

func legacyTaskDuePhysicalColumn(columns []string) string {
	for _, column := range columns {
		if column == "due_at" {
			return "due_at"
		}
	}
	for _, column := range columns {
		if column == "due" {
			return "due"
		}
	}
	return "due_at"
}

func validateLegacyTriggerInventory(inventory SchemaInventory, manifest []LegacySourceManifest) []TriggerPlanBlockReason {
	reasons := make([]TriggerPlanBlockReason, 0)
	for _, entry := range manifest {
		columns, tableExists := inventory.Tables[entry.Table]
		if !tableExists {
			reasons = append(reasons, TriggerPlanBlockReason{
				Code: TriggerPlanMissingTable, EntityKind: entry.EntityKind, Table: entry.Table,
			})
			continue
		}

		available := make(map[string]struct{}, len(columns))
		for _, column := range columns {
			available[column] = struct{}{}
		}
		missing := make([]string, 0)
		for _, required := range entry.RequiredColumns {
			_, ok := available[required]
			if !ok && entry.Table == "tasks" && required == "due_at" {
				_, ok = available["due"]
			}
			if !ok {
				missing = append(missing, required)
			}
		}
		sort.Strings(missing)
		for _, column := range missing {
			reasons = append(reasons, TriggerPlanBlockReason{
				Code: TriggerPlanMissingColumn, EntityKind: entry.EntityKind, Table: entry.Table, Column: column,
			})
		}
	}
	return reasons
}

func triggerSpec(entry LegacySourceManifest, operation TriggerOperation, image TriggerImageRequirement) TriggerSpec {
	return TriggerSpec{
		EntityKind:                   entry.EntityKind,
		Table:                        entry.Table,
		WorkspaceColumn:              entry.WorkspaceColumn,
		IdentityColumns:              append([]string(nil), entry.IdentityColumns...),
		DependsOn:                    append([]ReplayEntityKind(nil), entry.DependsOn...),
		DependencyOrder:              entry.DependencyOrder,
		RequiredColumns:              append([]string(nil), entry.RequiredColumns...),
		Operation:                    operation,
		ImageRequirement:             image,
		RequiresLogicalVersionLedger: true,
		RequiresWorkspaceFence:       true,
	}
}

func cloneLegacyManifest(source []LegacySourceManifest) []LegacySourceManifest {
	result := make([]LegacySourceManifest, len(source))
	for index, entry := range source {
		result[index] = entry
		result[index].IdentityColumns = append([]string(nil), entry.IdentityColumns...)
		result[index].DependsOn = append([]ReplayEntityKind(nil), entry.DependsOn...)
		result[index].RequiredColumns = append([]string(nil), entry.RequiredColumns...)
	}
	return result
}
