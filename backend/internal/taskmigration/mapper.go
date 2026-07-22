package taskmigration

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type LegacyExecutionType string

const (
	LegacyExecutionSingle    LegacyExecutionType = "single"
	LegacyExecutionRecurring LegacyExecutionType = "recurring"
)

type LegacyEntityKind string

const (
	LegacyEntityProject     LegacyEntityKind = "project"
	LegacyEntityTask        LegacyEntityKind = "task"
	LegacyEntityRule        LegacyEntityKind = "rule"
	LegacyEntityOccurrence  LegacyEntityKind = "occurrence"
	LegacyEntityEvent       LegacyEntityKind = "event"
	LegacyEntityRoadmap     LegacyEntityKind = "roadmap"
	LegacyEntityRoadmapNode LegacyEntityKind = "roadmap_node"
	LegacyEntityRoadmapEdge LegacyEntityKind = "roadmap_edge"
)

type MapperBlockCode string

const (
	MapperBlockDuplicateIdentity       MapperBlockCode = "duplicate_identity"
	MapperBlockInvalidIdentity         MapperBlockCode = "invalid_identity"
	MapperBlockMissingPreflight        MapperBlockCode = "missing_preflight_decision"
	MapperBlockMissingRule             MapperBlockCode = "missing_recurring_rule"
	MapperBlockUnexpectedRule          MapperBlockCode = "unexpected_recurring_rule"
	MapperBlockMissingCompletedAt      MapperBlockCode = "missing_completed_at"
	MapperBlockInvalidPriority         MapperBlockCode = "invalid_priority"
	MapperBlockInvalidStatus           MapperBlockCode = "invalid_execution_status"
	MapperBlockInvalidTimezone         MapperBlockCode = "invalid_timezone"
	MapperBlockInvalidEventRange       MapperBlockCode = "invalid_event_range"
	MapperBlockInvalidAllDay           MapperBlockCode = "invalid_all_day_boundary"
	MapperBlockMissingProject          MapperBlockCode = "missing_project"
	MapperBlockOrphanSourceRow         MapperBlockCode = "orphan_source_row"
	MapperBlockInvalidRoadmapStructure MapperBlockCode = "invalid_roadmap_structure"
	MapperBlockMissingBlockedMetadata  MapperBlockCode = "missing_blocked_metadata"
)

type MapperBlock struct {
	Code      MapperBlockCode
	Reference string
	Detail    string
}

func (e *MapperBlock) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reference)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Reference, e.Detail)
}

type LegacyProjectRow struct {
	ID   string
	Name string
	Type LegacyProjectType
}

type LegacyTaskRow struct {
	ID            string
	ProjectID     string
	RoadmapNodeID string
	ExecutionType LegacyExecutionType
	Title         string
	Content       string
	Priority      int
	SortOrder     int64
	PlannedDate   string
	DueAt         *time.Time
	Status        taskdomain.ExecutionStatus
	Done          bool
	CompletedAt   *time.Time
	UpdatedAt     time.Time
	NoteID        string
}

type LegacyRuleRow struct {
	ID              string
	TaskID          string
	RecurrenceType  taskdomain.RecurrenceType
	TimingType      taskdomain.TimingType
	Timezone        string
	StartsOn        string
	EndsOn          string
	Interval        int
	Weekdays        []int
	MonthDays       []int
	LocalStartTime  string
	DurationMinutes int
}

type LegacyOccurrenceRow struct {
	ID             string
	TaskID         string
	OccurrenceDate string
	Status         taskdomain.ExecutionStatus
	CompletedAt    *time.Time
	UpdatedAt      time.Time
	DueAt          *time.Time
	Note           string
	BlockedReason  string
	NextAction     string
}

type LegacyRoadmapRow struct {
	ID        string
	ProjectID string
	Title     string
	Goal      string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type LegacyRoadmapNodeRow struct {
	ID                   string
	RoadmapID            string
	ParentID             string
	Type                 string
	Title                string
	Description          string
	PathType             string
	Status               string
	Deliverable          string
	AcceptanceCriteria   string
	CanvasX              float64
	CanvasY              float64
	OrderIndex           int
	ArticleSearchQueries []string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type LegacyRoadmapEdgeRow struct {
	ID           string
	RoadmapID    string
	SourceNodeID string
	TargetNodeID string
	Style        string
	CreatedAt    time.Time
}

type LegacyEventRow struct {
	ID          string
	ProjectID   string
	Title       string
	Description string
	StartAt     time.Time
	EndAt       time.Time
	AllDay      bool
	Location    string
	Kind        string
	Notes       string
	NoteID      string
}

type LegacyTaskDomainRows struct {
	Projects     []LegacyProjectRow
	Roadmaps     []LegacyRoadmapRow
	RoadmapNodes []LegacyRoadmapNodeRow
	RoadmapEdges []LegacyRoadmapEdgeRow
	Tasks        []LegacyTaskRow
	Rules        []LegacyRuleRow
	Occurrences  []LegacyOccurrenceRow
	Events       []LegacyEventRow
}

type V2ProjectProjection struct {
	ID         string
	Name       string
	Kind       string
	Horizon    string
	SystemRole string
	Generated  bool
}

type V2TaskProjection struct {
	ID              string
	ProjectID       string
	RoadmapNodeID   string
	Title           string
	Description     string
	Priority        int
	SortOrder       int64
	TaskNoteID      string
	LifecycleStatus taskdomain.TaskLifecycleStatus
}

type V2LearningRoadmapProjection struct {
	ID          string
	ProjectID   string
	Status      string
	Title       string
	Description string
}

type V2RoadmapNodeProjection struct {
	ID                   string
	ProjectID            string
	RoadmapID            string
	ParentID             string
	Title                string
	Description          string
	NodeType             string
	Status               string
	Position             float64
	LegacyNodeType       string
	PathType             string
	Deliverable          string
	AcceptanceCriteria   string
	CanvasX              float64
	CanvasY              float64
	ArticleSearchQueries []string
}

type V2RoadmapEdgeProjection struct {
	ID         string
	ProjectID  string
	RoadmapID  string
	FromNodeID string
	ToNodeID   string
	EdgeType   string
}

type V2ScheduleProjection struct {
	TaskID          string
	RecurrenceType  taskdomain.RecurrenceType
	TimingType      taskdomain.TimingType
	Timezone        string
	StartsOn        string
	EndsOn          string
	Interval        int
	Weekdays        []int
	MonthDays       []int
	LocalStartTime  string
	DurationMinutes int
}

type V2OccurrenceProjection struct {
	ID                        string
	TaskID                    string
	OccurrenceKey             string
	ExecutionStatus           taskdomain.ExecutionStatus
	PlannedDate               string
	AllDayEndDate             string
	PlannedStartAt            *time.Time
	PlannedEndAt              *time.Time
	DueAt                     *time.Time
	CompletedAt               *time.Time
	BlockedReason             string
	NextAction                string
	Location                  string
	CalendarKind              string
	CalendarNotes             string
	OccurrenceNoteID          string
	GeneratedScheduleRevision int64
}

type V2IDMapEntry struct {
	LegacyKind          LegacyEntityKind
	LegacyID            string
	TargetProjectID     string
	TargetTaskID        string
	TargetScheduleID    string
	TargetOccurrenceID  string
	TargetRoadmapID     string
	TargetRoadmapNodeID string
	TargetRoadmapEdgeID string
}

type V2Projection struct {
	Projects     []V2ProjectProjection
	Roadmaps     []V2LearningRoadmapProjection
	RoadmapNodes []V2RoadmapNodeProjection
	RoadmapEdges []V2RoadmapEdgeProjection
	Tasks        []V2TaskProjection
	Schedules    []V2ScheduleProjection
	Occurrences  []V2OccurrenceProjection
	IDMap        []V2IDMapEntry
}

// MapLegacyTaskDomain is deterministic and side-effect free. A provider can
// feed it rows from one consistent snapshot, then persist the complete result
// in a separate transaction only after this function succeeds.
func MapLegacyTaskDomain(preflight PreflightResult, rows LegacyTaskDomainRows) (V2Projection, error) {
	location, err := time.LoadLocation(preflight.MigrationTimezone)
	if err != nil {
		return V2Projection{}, &MapperBlock{Code: MapperBlockInvalidTimezone, Reference: preflight.MigrationTimezone}
	}

	projection, projectTargets, legacyProjectTargets, personalProjectID, err := projectProjection(preflight, rows.Projects)
	if err != nil {
		return V2Projection{}, err
	}
	roadmapNodeProjects, err := projectRoadmapGraph(&projection, rows.Roadmaps, rows.RoadmapNodes, rows.RoadmapEdges, legacyProjectTargets)
	if err != nil {
		return V2Projection{}, err
	}
	taskDecisions, err := preflightTaskDecisions(preflight.Tasks, projectTargets)
	if err != nil {
		return V2Projection{}, err
	}
	rulesByTask, err := indexLegacyRules(rows.Rules)
	if err != nil {
		return V2Projection{}, err
	}
	occurrencesByTask, err := indexLegacyOccurrences(rows.Occurrences)
	if err != nil {
		return V2Projection{}, err
	}
	events, err := sortedUniqueEvents(rows.Events)
	if err != nil {
		return V2Projection{}, err
	}
	tasks, err := sortedUniqueTasks(rows.Tasks)
	if err != nil {
		return V2Projection{}, err
	}

	targetTaskIDs := make(map[string]struct{}, len(tasks)+len(events))
	targetOccurrenceIDs := make(map[string]struct{}, len(tasks)+len(rows.Occurrences)+len(events))
	usedRules := make(map[string]struct{}, len(rulesByTask))
	usedOccurrenceTasks := make(map[string]struct{}, len(occurrencesByTask))

	for _, legacyTask := range tasks {
		decision, ok := taskDecisions[legacyTask.ID]
		if !ok {
			return V2Projection{}, &MapperBlock{Code: MapperBlockMissingPreflight, Reference: legacyTask.ID, Detail: "task"}
		}
		if legacyTask.Priority < 0 || legacyTask.Priority > 3 {
			return V2Projection{}, &MapperBlock{Code: MapperBlockInvalidPriority, Reference: legacyTask.ID}
		}
		if legacyTask.RoadmapNodeID != "" {
			nodeProjectID, ok := roadmapNodeProjects[legacyTask.RoadmapNodeID]
			if !ok || nodeProjectID != decision.TargetProjectID {
				return V2Projection{}, &MapperBlock{
					Code: MapperBlockInvalidRoadmapStructure, Reference: legacyTask.ID,
					Detail: "roadmap_node=" + legacyTask.RoadmapNodeID + " task_project=" + decision.TargetProjectID,
				}
			}
		}
		if err := reserveTargetID(targetTaskIDs, legacyTask.ID, "task"); err != nil {
			return V2Projection{}, err
		}

		executionType := legacyTask.ExecutionType
		if executionType == "" {
			executionType = LegacyExecutionSingle
		}
		switch executionType {
		case LegacyExecutionSingle:
			if _, hasRule := rulesByTask[legacyTask.ID]; hasRule {
				return V2Projection{}, &MapperBlock{Code: MapperBlockUnexpectedRule, Reference: legacyTask.ID}
			}
			task, schedule, occurrence, err := mapSingleTask(legacyTask, decision.TargetProjectID, preflight.MigrationTimezone)
			if err != nil {
				return V2Projection{}, err
			}
			if err := reserveTargetID(targetOccurrenceIDs, occurrence.ID, "occurrence"); err != nil {
				return V2Projection{}, err
			}
			projection.Tasks = append(projection.Tasks, task)
			projection.Schedules = append(projection.Schedules, schedule)
			projection.Occurrences = append(projection.Occurrences, occurrence)
			projection.IDMap = append(projection.IDMap, V2IDMapEntry{
				LegacyKind: LegacyEntityTask, LegacyID: legacyTask.ID,
				TargetProjectID: decision.TargetProjectID, TargetTaskID: legacyTask.ID,
				TargetScheduleID: legacyTask.ID, TargetOccurrenceID: occurrence.ID,
			})
		case LegacyExecutionRecurring:
			rule, ok := rulesByTask[legacyTask.ID]
			if !ok {
				return V2Projection{}, &MapperBlock{Code: MapperBlockMissingRule, Reference: legacyTask.ID}
			}
			usedRules[legacyTask.ID] = struct{}{}
			task, schedule, err := mapRecurringTask(legacyTask, decision.TargetProjectID, rule, preflight.MigrationTimezone)
			if err != nil {
				return V2Projection{}, err
			}
			projection.Tasks = append(projection.Tasks, task)
			projection.Schedules = append(projection.Schedules, schedule)
			projection.IDMap = append(projection.IDMap, V2IDMapEntry{
				LegacyKind: LegacyEntityTask, LegacyID: legacyTask.ID,
				TargetProjectID: decision.TargetProjectID, TargetTaskID: legacyTask.ID,
			})
			projection.IDMap = append(projection.IDMap, V2IDMapEntry{
				LegacyKind: LegacyEntityRule, LegacyID: rule.ID,
				TargetProjectID: decision.TargetProjectID, TargetTaskID: legacyTask.ID, TargetScheduleID: legacyTask.ID,
			})
			legacyOccurrences := occurrencesByTask[legacyTask.ID]
			if len(legacyOccurrences) > 0 {
				usedOccurrenceTasks[legacyTask.ID] = struct{}{}
			}
			for _, legacyOccurrence := range legacyOccurrences {
				occurrence, err := mapRecurringOccurrence(legacyTask, legacyOccurrence)
				if err != nil {
					return V2Projection{}, err
				}
				if err := reserveTargetID(targetOccurrenceIDs, occurrence.ID, "occurrence"); err != nil {
					return V2Projection{}, err
				}
				projection.Occurrences = append(projection.Occurrences, occurrence)
				projection.IDMap = append(projection.IDMap, V2IDMapEntry{
					LegacyKind: LegacyEntityOccurrence, LegacyID: legacyOccurrenceIdentity(legacyOccurrence),
					TargetProjectID: decision.TargetProjectID, TargetTaskID: legacyTask.ID, TargetOccurrenceID: occurrence.ID,
				})
			}
		default:
			return V2Projection{}, &MapperBlock{Code: MapperBlockMissingRule, Reference: legacyTask.ID, Detail: "unknown execution_type=" + string(executionType)}
		}
	}

	for taskID := range rulesByTask {
		if _, used := usedRules[taskID]; !used {
			return V2Projection{}, &MapperBlock{Code: MapperBlockOrphanSourceRow, Reference: taskID, Detail: "rule"}
		}
	}
	for taskID := range occurrencesByTask {
		if _, used := usedOccurrenceTasks[taskID]; !used {
			return V2Projection{}, &MapperBlock{Code: MapperBlockOrphanSourceRow, Reference: taskID, Detail: "occurrence"}
		}
	}

	for _, event := range events {
		targetProjectID := personalProjectID
		if event.ProjectID != "" {
			var ok bool
			targetProjectID, ok = legacyProjectTargets[event.ProjectID]
			if !ok {
				return V2Projection{}, &MapperBlock{Code: MapperBlockMissingProject, Reference: event.ID, Detail: "project=" + event.ProjectID}
			}
		}
		task, schedule, occurrence, err := mapLegacyEvent(event, targetProjectID, preflight.MigrationTimezone, location)
		if err != nil {
			return V2Projection{}, err
		}
		if err := reserveTargetID(targetTaskIDs, task.ID, "task"); err != nil {
			return V2Projection{}, err
		}
		if err := reserveTargetID(targetOccurrenceIDs, occurrence.ID, "occurrence"); err != nil {
			return V2Projection{}, err
		}
		projection.Tasks = append(projection.Tasks, task)
		projection.Schedules = append(projection.Schedules, schedule)
		projection.Occurrences = append(projection.Occurrences, occurrence)
		projection.IDMap = append(projection.IDMap, V2IDMapEntry{
			LegacyKind: LegacyEntityEvent, LegacyID: event.ID,
			TargetProjectID: targetProjectID, TargetTaskID: task.ID,
			TargetScheduleID: task.ID, TargetOccurrenceID: occurrence.ID,
		})
	}

	if err := validateUniqueIDMap(projection.IDMap); err != nil {
		return V2Projection{}, err
	}
	sortProjection(&projection)
	return projection, nil
}

func projectProjection(preflight PreflightResult, rows []LegacyProjectRow) (V2Projection, map[string]struct{}, map[string]string, string, error) {
	rowIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, duplicate := rowIDs[row.ID]; duplicate {
			return V2Projection{}, nil, nil, "", &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: row.ID, Detail: "project"}
		}
		rowIDs[row.ID] = struct{}{}
	}
	projection := V2Projection{Projects: make([]V2ProjectProjection, 0, len(preflight.Projects))}
	projectTargets := make(map[string]struct{}, len(preflight.Projects))
	legacyTargets := make(map[string]string, len(preflight.Projects))
	personalProjectID := ""
	for _, decision := range preflight.Projects {
		if _, duplicate := projectTargets[decision.TargetID]; duplicate {
			return V2Projection{}, nil, nil, "", &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: decision.TargetID, Detail: "preflight project target"}
		}
		if !decision.Generated {
			if _, exists := rowIDs[decision.LegacyID]; !exists {
				return V2Projection{}, nil, nil, "", &MapperBlock{Code: MapperBlockMissingPreflight, Reference: decision.LegacyID, Detail: "project row"}
			}
			legacyTargets[decision.LegacyID] = decision.TargetID
			projection.IDMap = append(projection.IDMap, V2IDMapEntry{
				LegacyKind: LegacyEntityProject, LegacyID: decision.LegacyID, TargetProjectID: decision.TargetID,
			})
		}
		projectTargets[decision.TargetID] = struct{}{}
		if decision.SystemRole == "personal" {
			personalProjectID = decision.TargetID
		}
		projection.Projects = append(projection.Projects, V2ProjectProjection{
			ID: decision.TargetID, Name: decision.Name, Kind: decision.Kind, Horizon: decision.Horizon,
			SystemRole: decision.SystemRole, Generated: decision.Generated,
		})
	}
	for legacyID := range rowIDs {
		if _, mapped := legacyTargets[legacyID]; !mapped {
			return V2Projection{}, nil, nil, "", &MapperBlock{
				Code: MapperBlockMissingPreflight, Reference: legacyID, Detail: "project decision",
			}
		}
	}
	if personalProjectID == "" {
		return V2Projection{}, nil, nil, "", &MapperBlock{Code: MapperBlockMissingProject, Reference: "personal", Detail: "system role"}
	}
	return projection, projectTargets, legacyTargets, personalProjectID, nil
}

func projectRoadmapGraph(
	projection *V2Projection,
	roadmaps []LegacyRoadmapRow,
	nodes []LegacyRoadmapNodeRow,
	edges []LegacyRoadmapEdgeRow,
	legacyProjectTargets map[string]string,
) (map[string]string, error) {
	projectKinds := make(map[string]string, len(projection.Projects))
	for _, project := range projection.Projects {
		projectKinds[project.ID] = project.Kind
	}

	orderedRoadmaps := append([]LegacyRoadmapRow(nil), roadmaps...)
	sort.Slice(orderedRoadmaps, func(i, j int) bool { return orderedRoadmaps[i].ID < orderedRoadmaps[j].ID })
	roadmapProjects := make(map[string]string, len(orderedRoadmaps))
	projectRoadmaps := make(map[string]string, len(orderedRoadmaps))
	for _, legacy := range orderedRoadmaps {
		if strings.TrimSpace(legacy.ID) == "" || strings.TrimSpace(legacy.ProjectID) == "" {
			return nil, &MapperBlock{Code: MapperBlockInvalidIdentity, Reference: legacy.ID, Detail: "roadmap requires id and project id"}
		}
		if _, duplicate := roadmapProjects[legacy.ID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: legacy.ID, Detail: "roadmap"}
		}
		projectID, ok := legacyProjectTargets[legacy.ProjectID]
		if !ok || projectKinds[projectID] != "learning" {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: "roadmap project must be learning: " + legacy.ProjectID}
		}
		if existing, duplicate := projectRoadmaps[projectID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: "project already owns roadmap " + existing}
		}
		status, err := mapLegacyRoadmapStatus(legacy.Status)
		if err != nil {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: err.Error()}
		}
		roadmapProjects[legacy.ID] = projectID
		projectRoadmaps[projectID] = legacy.ID
		projection.Roadmaps = append(projection.Roadmaps, V2LearningRoadmapProjection{
			ID: legacy.ID, ProjectID: projectID, Status: status, Title: legacy.Title, Description: legacy.Goal,
		})
		projection.IDMap = append(projection.IDMap, V2IDMapEntry{
			LegacyKind: LegacyEntityRoadmap, LegacyID: legacy.ID,
			TargetProjectID: projectID, TargetRoadmapID: legacy.ID,
		})
	}

	projectedNodes := make([]V2RoadmapNodeProjection, 0, len(nodes))
	nodeProjects := make(map[string]string, len(nodes))
	nodeRoadmaps := make(map[string]string, len(nodes))
	for _, legacy := range nodes {
		if strings.TrimSpace(legacy.ID) == "" || strings.TrimSpace(legacy.RoadmapID) == "" {
			return nil, &MapperBlock{Code: MapperBlockInvalidIdentity, Reference: legacy.ID, Detail: "roadmap node requires id and roadmap id"}
		}
		if _, duplicate := nodeProjects[legacy.ID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: legacy.ID, Detail: "roadmap node"}
		}
		projectID, ok := roadmapProjects[legacy.RoadmapID]
		if !ok {
			return nil, &MapperBlock{Code: MapperBlockOrphanSourceRow, Reference: legacy.ID, Detail: "roadmap node roadmap=" + legacy.RoadmapID}
		}
		nodeType, err := mapLegacyRoadmapNodeType(legacy.Type)
		if err != nil {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: err.Error()}
		}
		status, err := mapLegacyRoadmapNodeStatus(legacy.Status)
		if err != nil {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: err.Error()}
		}
		nodeProjects[legacy.ID] = projectID
		nodeRoadmaps[legacy.ID] = legacy.RoadmapID
		projectedNodes = append(projectedNodes, V2RoadmapNodeProjection{
			ID: legacy.ID, ProjectID: projectID, RoadmapID: legacy.RoadmapID, ParentID: legacy.ParentID,
			Title: legacy.Title, Description: legacy.Description, NodeType: nodeType, Status: status,
			Position: float64(legacy.OrderIndex), LegacyNodeType: legacy.Type, PathType: legacy.PathType,
			Deliverable: legacy.Deliverable, AcceptanceCriteria: legacy.AcceptanceCriteria,
			CanvasX: legacy.CanvasX, CanvasY: legacy.CanvasY,
			ArticleSearchQueries: append([]string(nil), legacy.ArticleSearchQueries...),
		})
	}
	orderedNodes, err := topologicallySortedV2RoadmapNodes(projectedNodes)
	if err != nil {
		return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: "roadmap_nodes", Detail: err.Error()}
	}
	projection.RoadmapNodes = append(projection.RoadmapNodes, orderedNodes...)
	for _, node := range orderedNodes {
		projection.IDMap = append(projection.IDMap, V2IDMapEntry{
			LegacyKind: LegacyEntityRoadmapNode, LegacyID: node.ID,
			TargetProjectID: node.ProjectID, TargetRoadmapNodeID: node.ID,
		})
	}

	orderedEdges := append([]LegacyRoadmapEdgeRow(nil), edges...)
	sort.Slice(orderedEdges, func(i, j int) bool { return orderedEdges[i].ID < orderedEdges[j].ID })
	seenEdges := make(map[string]struct{}, len(orderedEdges))
	for _, legacy := range orderedEdges {
		if strings.TrimSpace(legacy.ID) == "" || strings.TrimSpace(legacy.RoadmapID) == "" || strings.TrimSpace(legacy.SourceNodeID) == "" || strings.TrimSpace(legacy.TargetNodeID) == "" {
			return nil, &MapperBlock{Code: MapperBlockInvalidIdentity, Reference: legacy.ID, Detail: "roadmap edge requires id, roadmap, and endpoints"}
		}
		if _, duplicate := seenEdges[legacy.ID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: legacy.ID, Detail: "roadmap edge"}
		}
		seenEdges[legacy.ID] = struct{}{}
		projectID, ok := roadmapProjects[legacy.RoadmapID]
		if !ok || nodeRoadmaps[legacy.SourceNodeID] != legacy.RoadmapID || nodeRoadmaps[legacy.TargetNodeID] != legacy.RoadmapID || legacy.SourceNodeID == legacy.TargetNodeID {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: "edge endpoints must belong to one roadmap"}
		}
		edgeType, err := mapLegacyRoadmapEdgeStyle(legacy.Style)
		if err != nil {
			return nil, &MapperBlock{Code: MapperBlockInvalidRoadmapStructure, Reference: legacy.ID, Detail: err.Error()}
		}
		projection.RoadmapEdges = append(projection.RoadmapEdges, V2RoadmapEdgeProjection{
			ID: legacy.ID, ProjectID: projectID, RoadmapID: legacy.RoadmapID,
			FromNodeID: legacy.SourceNodeID, ToNodeID: legacy.TargetNodeID, EdgeType: edgeType,
		})
		projection.IDMap = append(projection.IDMap, V2IDMapEntry{
			LegacyKind: LegacyEntityRoadmapEdge, LegacyID: legacy.ID,
			TargetProjectID: projectID, TargetRoadmapEdgeID: legacy.ID,
		})
	}
	return nodeProjects, nil
}

func mapLegacyRoadmapStatus(status string) (string, error) {
	switch status {
	case "", "draft", "ready":
		return "draft", nil
	case "active":
		return "active", nil
	case "done", "completed":
		return "completed", nil
	case "failed":
		return "failed", nil
	case "archived":
		return "archived", nil
	default:
		return "", fmt.Errorf("invalid roadmap status %q", status)
	}
}

func mapLegacyRoadmapNodeType(nodeType string) (string, error) {
	switch nodeType {
	case "phase":
		return "stage", nil
	case "module", "task", "":
		return "topic", nil
	case "choice":
		return "milestone", nil
	default:
		return "", fmt.Errorf("invalid roadmap node type %q", nodeType)
	}
}

func mapLegacyRoadmapNodeStatus(status string) (string, error) {
	switch status {
	case "", "todo":
		return "available", nil
	case "active":
		return "in_progress", nil
	case "done":
		return "mastered", nil
	case "skipped":
		return "skipped", nil
	default:
		return "", fmt.Errorf("invalid roadmap node status %q", status)
	}
}

func mapLegacyRoadmapEdgeStyle(style string) (string, error) {
	switch style {
	case "", "solid":
		return "suggested_order", nil
	case "dotted":
		return "related", nil
	default:
		return "", fmt.Errorf("invalid roadmap edge style %q", style)
	}
}

func topologicallySortedV2RoadmapNodes(nodes []V2RoadmapNodeProjection) ([]V2RoadmapNodeProjection, error) {
	indexed := make(map[string]V2RoadmapNodeProjection, len(nodes))
	for _, node := range nodes {
		if _, duplicate := indexed[node.ID]; duplicate {
			return nil, fmt.Errorf("duplicate roadmap node %s", node.ID)
		}
		indexed[node.ID] = node
	}
	orderedInput := append([]V2RoadmapNodeProjection(nil), nodes...)
	sort.Slice(orderedInput, func(i, j int) bool {
		if orderedInput[i].RoadmapID != orderedInput[j].RoadmapID {
			return orderedInput[i].RoadmapID < orderedInput[j].RoadmapID
		}
		if orderedInput[i].Position != orderedInput[j].Position {
			return orderedInput[i].Position < orderedInput[j].Position
		}
		return orderedInput[i].ID < orderedInput[j].ID
	})
	states := make(map[string]uint8, len(nodes))
	result := make([]V2RoadmapNodeProjection, 0, len(nodes))
	var visit func(V2RoadmapNodeProjection) error
	visit = func(node V2RoadmapNodeProjection) error {
		switch states[node.ID] {
		case 1:
			return fmt.Errorf("roadmap parent cycle at %s", node.ID)
		case 2:
			return nil
		}
		states[node.ID] = 1
		if node.ParentID != "" {
			parent, ok := indexed[node.ParentID]
			if !ok {
				return fmt.Errorf("roadmap node %s has missing parent %s", node.ID, node.ParentID)
			}
			if parent.ProjectID != node.ProjectID || parent.RoadmapID != node.RoadmapID {
				return fmt.Errorf("roadmap node %s parent crosses project or roadmap", node.ID)
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		states[node.ID] = 2
		result = append(result, node)
		return nil
	}
	for _, node := range orderedInput {
		if err := visit(node); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func preflightTaskDecisions(decisions []TaskDecision, projectTargets map[string]struct{}) (map[string]TaskDecision, error) {
	indexed := make(map[string]TaskDecision, len(decisions))
	for _, decision := range decisions {
		if _, duplicate := indexed[decision.LegacyID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: decision.LegacyID, Detail: "preflight task"}
		}
		if _, exists := projectTargets[decision.TargetProjectID]; !exists {
			return nil, &MapperBlock{Code: MapperBlockMissingProject, Reference: decision.LegacyID, Detail: "target=" + decision.TargetProjectID}
		}
		indexed[decision.LegacyID] = decision
	}
	return indexed, nil
}

func sortedUniqueTasks(rows []LegacyTaskRow) ([]LegacyTaskRow, error) {
	ordered := append([]LegacyTaskRow(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].ID == ordered[index].ID {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: ordered[index].ID, Detail: "task"}
		}
	}
	return ordered, nil
}

func sortedUniqueEvents(rows []LegacyEventRow) ([]LegacyEventRow, error) {
	ordered := append([]LegacyEventRow(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].ID == ordered[index].ID {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: ordered[index].ID, Detail: "event"}
		}
	}
	return ordered, nil
}

func indexLegacyRules(rows []LegacyRuleRow) (map[string]LegacyRuleRow, error) {
	byID := make(map[string]struct{}, len(rows))
	byTask := make(map[string]LegacyRuleRow, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.ID) == "" || strings.TrimSpace(row.TaskID) == "" {
			return nil, &MapperBlock{Code: MapperBlockInvalidIdentity, Reference: row.ID, Detail: "recurrence rule requires id and task id"}
		}
		if _, duplicate := byID[row.ID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: row.ID, Detail: "rule"}
		}
		byID[row.ID] = struct{}{}
		if _, duplicate := byTask[row.TaskID]; duplicate {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: row.TaskID, Detail: "task rule"}
		}
		byTask[row.TaskID] = row
	}
	return byTask, nil
}

func indexLegacyOccurrences(rows []LegacyOccurrenceRow) (map[string][]LegacyOccurrenceRow, error) {
	ordered := append([]LegacyOccurrenceRow(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool {
		left := legacyOccurrenceIdentity(ordered[i])
		right := legacyOccurrenceIdentity(ordered[j])
		return left < right
	})
	byTask := make(map[string][]LegacyOccurrenceRow)
	lastIdentity := ""
	for _, row := range ordered {
		identity := legacyOccurrenceIdentity(row)
		if identity == lastIdentity && identity != "" {
			return nil, &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: identity, Detail: "occurrence"}
		}
		lastIdentity = identity
		byTask[row.TaskID] = append(byTask[row.TaskID], row)
	}
	return byTask, nil
}

func mapSingleTask(legacy LegacyTaskRow, projectID, timezone string) (V2TaskProjection, V2ScheduleProjection, V2OccurrenceProjection, error) {
	status := legacy.Status
	if status == "" {
		status = taskdomain.ExecutionStatusOpen
	}
	if legacy.Done {
		status = taskdomain.ExecutionStatusDone
	}
	completedAt, err := completionTime(status, legacy.CompletedAt, legacy.UpdatedAt, legacy.ID)
	if err != nil {
		return V2TaskProjection{}, V2ScheduleProjection{}, V2OccurrenceProjection{}, err
	}
	if !validExecutionStatus(status) {
		return V2TaskProjection{}, V2ScheduleProjection{}, V2OccurrenceProjection{}, &MapperBlock{Code: MapperBlockInvalidStatus, Reference: legacy.ID}
	}
	lifecycle := taskdomain.TaskLifecycleActive
	if status == taskdomain.ExecutionStatusDone {
		lifecycle = taskdomain.TaskLifecycleCompleted
	}
	timing := taskdomain.TimingUnscheduled
	if legacy.PlannedDate != "" {
		timing = taskdomain.TimingDate
	}
	occurrenceID := deterministicProjectionID("task-occurrence", legacy.ID, "once")
	return V2TaskProjection{
			ID: legacy.ID, ProjectID: projectID, RoadmapNodeID: legacy.RoadmapNodeID, Title: legacy.Title, Description: legacy.Content,
			Priority: legacy.Priority, SortOrder: legacy.SortOrder, TaskNoteID: legacy.NoteID, LifecycleStatus: lifecycle,
		}, V2ScheduleProjection{
			TaskID: legacy.ID, RecurrenceType: taskdomain.RecurrenceNone, TimingType: timing,
			Timezone: timezone, StartsOn: legacy.PlannedDate, Interval: 1,
		}, V2OccurrenceProjection{
			ID: occurrenceID, TaskID: legacy.ID, OccurrenceKey: "once", ExecutionStatus: status,
			PlannedDate: legacy.PlannedDate, DueAt: cloneTimePointer(legacy.DueAt), CompletedAt: completedAt,
			GeneratedScheduleRevision: 1,
		}, nil
}

func mapRecurringTask(legacy LegacyTaskRow, projectID string, rule LegacyRuleRow, fallbackTimezone string) (V2TaskProjection, V2ScheduleProjection, error) {
	if rule.RecurrenceType == "" || rule.RecurrenceType == taskdomain.RecurrenceNone {
		return V2TaskProjection{}, V2ScheduleProjection{}, &MapperBlock{Code: MapperBlockMissingRule, Reference: legacy.ID}
	}
	timezone := rule.Timezone
	if timezone == "" {
		timezone = fallbackTimezone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return V2TaskProjection{}, V2ScheduleProjection{}, &MapperBlock{Code: MapperBlockInvalidTimezone, Reference: timezone, Detail: "task=" + legacy.ID}
	}
	timing := rule.TimingType
	if timing == "" {
		timing = taskdomain.TimingUnscheduled
	}
	interval := rule.Interval
	if interval == 0 {
		interval = 1
	}
	return V2TaskProjection{
			ID: legacy.ID, ProjectID: projectID, RoadmapNodeID: legacy.RoadmapNodeID, Title: legacy.Title, Description: legacy.Content,
			Priority: legacy.Priority, SortOrder: legacy.SortOrder, TaskNoteID: legacy.NoteID, LifecycleStatus: taskdomain.TaskLifecycleActive,
		}, V2ScheduleProjection{
			TaskID: legacy.ID, RecurrenceType: rule.RecurrenceType, TimingType: timing, Timezone: timezone,
			StartsOn: rule.StartsOn, EndsOn: rule.EndsOn, Interval: interval,
			Weekdays: normalizedInts(rule.Weekdays), MonthDays: normalizedInts(rule.MonthDays),
			LocalStartTime: rule.LocalStartTime, DurationMinutes: rule.DurationMinutes,
		}, nil
}

func mapRecurringOccurrence(task LegacyTaskRow, legacy LegacyOccurrenceRow) (V2OccurrenceProjection, error) {
	status := legacy.Status
	if status == "" {
		status = taskdomain.ExecutionStatusOpen
	}
	if !validExecutionStatus(status) {
		return V2OccurrenceProjection{}, &MapperBlock{Code: MapperBlockInvalidStatus, Reference: legacyOccurrenceIdentity(legacy)}
	}
	if status == taskdomain.ExecutionStatusBlocked && (strings.TrimSpace(legacy.BlockedReason) == "" || strings.TrimSpace(legacy.NextAction) == "") {
		return V2OccurrenceProjection{}, &MapperBlock{Code: MapperBlockMissingBlockedMetadata, Reference: legacyOccurrenceIdentity(legacy)}
	}
	fallback := legacy.UpdatedAt
	if fallback.IsZero() {
		fallback = task.UpdatedAt
	}
	completedAt, err := completionTime(status, legacy.CompletedAt, fallback, legacyOccurrenceIdentity(legacy))
	if err != nil {
		return V2OccurrenceProjection{}, err
	}
	return V2OccurrenceProjection{
		ID:     deterministicProjectionID("task-occurrence", legacy.TaskID, legacy.OccurrenceDate),
		TaskID: legacy.TaskID, OccurrenceKey: legacy.OccurrenceDate, ExecutionStatus: status,
		PlannedDate: legacy.OccurrenceDate, DueAt: cloneTimePointer(legacy.DueAt), CompletedAt: completedAt,
		CalendarNotes: legacy.Note, BlockedReason: legacy.BlockedReason, NextAction: legacy.NextAction, GeneratedScheduleRevision: 1,
	}, nil
}

func mapLegacyEvent(event LegacyEventRow, projectID, timezone string, location *time.Location) (V2TaskProjection, V2ScheduleProjection, V2OccurrenceProjection, error) {
	if !event.EndAt.After(event.StartAt) {
		return V2TaskProjection{}, V2ScheduleProjection{}, V2OccurrenceProjection{}, &MapperBlock{Code: MapperBlockInvalidEventRange, Reference: event.ID}
	}
	taskID := deterministicProjectionID("event-task", event.ID)
	occurrenceID := deterministicProjectionID("event-occurrence", event.ID)
	timing := taskdomain.TimingTimeBlock
	startLocal := event.StartAt.In(location)
	plannedDate := startLocal.Format("2006-01-02")
	allDayEndDate := ""
	var plannedStartAt, plannedEndAt *time.Time
	if event.AllDay {
		timing = taskdomain.TimingDate
		if !isLocalMidnight(event.StartAt, location) || !isLocalMidnight(event.EndAt, location) {
			return V2TaskProjection{}, V2ScheduleProjection{}, V2OccurrenceProjection{}, &MapperBlock{Code: MapperBlockInvalidAllDay, Reference: event.ID}
		}
		allDayEndDate = event.EndAt.In(location).Format("2006-01-02")
	} else {
		plannedStartAt = cloneTimeValue(event.StartAt)
		plannedEndAt = cloneTimeValue(event.EndAt)
	}
	return V2TaskProjection{
			ID: taskID, ProjectID: projectID, Title: event.Title, Description: event.Description,
			LifecycleStatus: taskdomain.TaskLifecycleActive,
		}, V2ScheduleProjection{
			TaskID: taskID, RecurrenceType: taskdomain.RecurrenceNone, TimingType: timing,
			Timezone: timezone, StartsOn: plannedDate, Interval: 1,
		}, V2OccurrenceProjection{
			ID: occurrenceID, TaskID: taskID, OccurrenceKey: "once", ExecutionStatus: taskdomain.ExecutionStatusOpen,
			PlannedDate: plannedDate, AllDayEndDate: allDayEndDate,
			PlannedStartAt: plannedStartAt, PlannedEndAt: plannedEndAt,
			Location: event.Location, CalendarKind: event.Kind, CalendarNotes: event.Notes,
			OccurrenceNoteID: event.NoteID, GeneratedScheduleRevision: 1,
		}, nil
}

func completionTime(status taskdomain.ExecutionStatus, completedAt *time.Time, fallback time.Time, reference string) (*time.Time, error) {
	if status != taskdomain.ExecutionStatusDone {
		return nil, nil
	}
	if completedAt != nil {
		return cloneTimePointer(completedAt), nil
	}
	if fallback.IsZero() {
		return nil, &MapperBlock{Code: MapperBlockMissingCompletedAt, Reference: reference}
	}
	return cloneTimeValue(fallback), nil
}

func validExecutionStatus(status taskdomain.ExecutionStatus) bool {
	switch status {
	case taskdomain.ExecutionStatusOpen,
		taskdomain.ExecutionStatusActive,
		taskdomain.ExecutionStatusBlocked,
		taskdomain.ExecutionStatusDone,
		taskdomain.ExecutionStatusSkipped,
		taskdomain.ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

func normalizedInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func legacyOccurrenceIdentity(row LegacyOccurrenceRow) string {
	if row.ID != "" {
		return row.ID
	}
	return row.TaskID + "/" + row.OccurrenceDate
}

func deterministicProjectionID(prefix string, parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%s-%x", prefix, hash[:12])
}

func reserveTargetID(targets map[string]struct{}, id, kind string) error {
	if _, duplicate := targets[id]; duplicate {
		return &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: id, Detail: "target " + kind}
	}
	targets[id] = struct{}{}
	return nil
}

func validateUniqueIDMap(entries []V2IDMapEntry) error {
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		key := string(entry.LegacyKind) + "\x00" + entry.LegacyID
		if _, duplicate := seen[key]; duplicate {
			return &MapperBlock{Code: MapperBlockDuplicateIdentity, Reference: entry.LegacyID, Detail: "ID map " + string(entry.LegacyKind)}
		}
		seen[key] = struct{}{}
	}
	return nil
}

func sortProjection(projection *V2Projection) {
	sort.Slice(projection.Projects, func(i, j int) bool { return projection.Projects[i].ID < projection.Projects[j].ID })
	sort.Slice(projection.Roadmaps, func(i, j int) bool { return projection.Roadmaps[i].ID < projection.Roadmaps[j].ID })
	if ordered, err := topologicallySortedV2RoadmapNodes(projection.RoadmapNodes); err == nil {
		projection.RoadmapNodes = ordered
	}
	sort.Slice(projection.RoadmapEdges, func(i, j int) bool { return projection.RoadmapEdges[i].ID < projection.RoadmapEdges[j].ID })
	sort.Slice(projection.Tasks, func(i, j int) bool { return projection.Tasks[i].ID < projection.Tasks[j].ID })
	sort.Slice(projection.Schedules, func(i, j int) bool { return projection.Schedules[i].TaskID < projection.Schedules[j].TaskID })
	sort.Slice(projection.Occurrences, func(i, j int) bool { return projection.Occurrences[i].ID < projection.Occurrences[j].ID })
	sort.Slice(projection.IDMap, func(i, j int) bool {
		left := projection.IDMap[i]
		right := projection.IDMap[j]
		if left.LegacyKind != right.LegacyKind {
			return left.LegacyKind < right.LegacyKind
		}
		return left.LegacyID < right.LegacyID
	})
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return cloneTimeValue(*value)
}

func cloneTimeValue(value time.Time) *time.Time {
	clone := value
	return &clone
}
