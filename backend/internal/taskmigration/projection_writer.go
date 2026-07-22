package taskmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

var ErrInvalidProjectionWriteInput = errors.New("invalid task domain projection write input")

type ProjectionWriteConflictCode string

const (
	ProjectionWriteConflictMissingSourceVersion  ProjectionWriteConflictCode = "missing_source_version"
	ProjectionWriteConflictUnmappedSourceVersion ProjectionWriteConflictCode = "unmapped_source_version"
	ProjectionWriteConflictInvalidSourceVersion  ProjectionWriteConflictCode = "invalid_source_version"
	ProjectionWriteConflictMappingVersion        ProjectionWriteConflictCode = "mapping_version_conflict"
	ProjectionWriteConflictMappingTarget         ProjectionWriteConflictCode = "mapping_target_conflict"
	ProjectionWriteConflictTargetData            ProjectionWriteConflictCode = "target_data_conflict"
)

// ProjectionWriteConflictError is deliberately provider independent. A
// coordinator must not infer retry safety from a SQLite or PostgreSQL error
// string when a source version or target identity disagrees.
type ProjectionWriteConflictError struct {
	Code        ProjectionWriteConflictCode
	WorkspaceID string
	Reference   string
	Detail      string
}

func (e *ProjectionWriteConflictError) Error() string {
	message := fmt.Sprintf("task domain projection conflict: workspace=%s code=%s reference=%s", e.WorkspaceID, e.Code, e.Reference)
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *ProjectionWriteConflictError) Is(target error) bool {
	other, ok := target.(*ProjectionWriteConflictError)
	return ok && e.Code == other.Code
}

type ProjectionSourceVersion struct {
	EntityKind     LegacyEntityKind
	LegacyID       string
	LogicalVersion int64
}

type V2ProjectionWrite struct {
	WorkspaceID    string
	Projection     V2Projection
	SourceVersions []ProjectionSourceVersion
	WrittenAt      time.Time
}

// V2ProjectionWriter persists one already-mapped snapshot in a transaction
// owned by BackfillStore. It neither begins nor commits a transaction. This is
// important: projected rows, ID maps, and the backfill watermark must share
// one atomic boundary.
type V2ProjectionWriter struct {
	dialect Dialect
}

func NewV2ProjectionWriter(dialect Dialect) (*V2ProjectionWriter, error) {
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidProjectionWriteInput, dialect)
	}
	return &V2ProjectionWriter{dialect: dialect}, nil
}

// WriteSnapshot loads the logical versions visible in the caller's snapshot
// transaction and persists the matching projection. Coordinators should use
// this entry point for backfill so the legacy rows, version ledger, v2 rows,
// and ID map all describe the same database snapshot.
func (w *V2ProjectionWriter) WriteSnapshot(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projection V2Projection,
	writtenAt time.Time,
) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if w == nil || ctx == nil || tx == nil || workspaceID == "" || writtenAt.IsZero() {
		return fmt.Errorf("%w: writer, context, transaction, workspace, and written_at are required", ErrInvalidProjectionWriteInput)
	}

	query := `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`
	if w.dialect == DialectPostgres {
		query = `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
			WHERE workspace_id=$1 AND entity_kind=$2 AND entity_id=$3`
	}
	versions := make([]ProjectionSourceVersion, 0, len(projection.IDMap))
	seen := make(map[projectionSourceKey]struct{}, len(projection.IDMap))
	for _, entry := range projection.IDMap {
		key := projectionSourceKey{kind: entry.LegacyKind, id: strings.TrimSpace(entry.LegacyID)}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if !validLegacyProjectionKind(key.kind) || key.id == "" {
			return fmt.Errorf("%w: invalid ID-map source %s/%q", ErrInvalidProjectionWriteInput, key.kind, key.id)
		}

		var logicalVersion int64
		var deleted bool
		err := tx.QueryRowContext(ctx, query, workspaceID, key.kind, key.id).Scan(&logicalVersion, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			return projectionConflict(ProjectionWriteConflictMissingSourceVersion, workspaceID, projectionSourceReference(key), "durable source-version ledger row is missing")
		}
		if err != nil {
			return fmt.Errorf("read snapshot source version %s: %w", projectionSourceReference(key), err)
		}
		if deleted || logicalVersion <= 0 {
			return projectionConflict(ProjectionWriteConflictMappingVersion, workspaceID, projectionSourceReference(key),
				fmt.Sprintf("logical_version=%d deleted=%t", logicalVersion, deleted))
		}
		versions = append(versions, ProjectionSourceVersion{
			EntityKind: key.kind, LegacyID: key.id, LogicalVersion: logicalVersion,
		})
	}

	return w.Write(ctx, tx, V2ProjectionWrite{
		WorkspaceID: workspaceID, Projection: projection,
		SourceVersions: versions, WrittenAt: writtenAt,
	})
}

func (w *V2ProjectionWriter) Write(ctx context.Context, tx *sql.Tx, input V2ProjectionWrite) error {
	if w == nil || (w.dialect != DialectSQLite && w.dialect != DialectPostgres) || ctx == nil || tx == nil {
		return fmt.Errorf("%w: writer, context, and transaction are required", ErrInvalidProjectionWriteInput)
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" || input.WrittenAt.IsZero() {
		return fmt.Errorf("%w: workspace and written_at are required", ErrInvalidProjectionWriteInput)
	}

	versions, err := validateProjectionWrite(input)
	if err != nil {
		return err
	}
	if err := w.validateDurableSourceVersions(ctx, tx, input.WorkspaceID, versions); err != nil {
		return err
	}
	writtenAt := input.WrittenAt.UTC().Format(time.RFC3339Nano)

	for _, project := range input.Projection.Projects {
		if err := w.writeProject(ctx, tx, input.WorkspaceID, project, writtenAt); err != nil {
			return err
		}
	}
	for _, roadmap := range input.Projection.Roadmaps {
		if err := w.writeRoadmap(ctx, tx, input.WorkspaceID, roadmap, writtenAt); err != nil {
			return err
		}
	}
	orderedNodes, err := topologicallySortedV2RoadmapNodes(input.Projection.RoadmapNodes)
	if err != nil {
		return projectionConflict(ProjectionWriteConflictTargetData, input.WorkspaceID, "roadmap_nodes", err.Error())
	}
	for _, node := range orderedNodes {
		if err := w.writeRoadmapNode(ctx, tx, input.WorkspaceID, node, writtenAt); err != nil {
			return err
		}
	}
	for _, edge := range input.Projection.RoadmapEdges {
		if err := w.writeRoadmapEdge(ctx, tx, input.WorkspaceID, edge, writtenAt); err != nil {
			return err
		}
	}
	for _, task := range input.Projection.Tasks {
		if err := w.writeTask(ctx, tx, input.WorkspaceID, task, writtenAt); err != nil {
			return err
		}
	}
	for _, schedule := range input.Projection.Schedules {
		if err := w.writeScheduleHeader(ctx, tx, input.WorkspaceID, schedule, writtenAt); err != nil {
			return err
		}
	}
	for _, schedule := range input.Projection.Schedules {
		normalized, err := normalizeProjectedSchedule(schedule, input.Projection.Occurrences)
		if err != nil {
			return projectionConflict(ProjectionWriteConflictTargetData, input.WorkspaceID, "schedule/"+schedule.TaskID, err.Error())
		}
		if err := w.writeScheduleVersion(ctx, tx, input.WorkspaceID, normalized, writtenAt); err != nil {
			return err
		}
	}
	for _, occurrence := range input.Projection.Occurrences {
		if err := w.writeOccurrence(ctx, tx, input.WorkspaceID, occurrence, writtenAt); err != nil {
			return err
		}
	}

	for _, entry := range input.Projection.IDMap {
		key := projectionSourceKey{kind: entry.LegacyKind, id: entry.LegacyID}
		for _, target := range projectionMapTargets(entry) {
			if err := w.writeIDMap(ctx, tx, input.WorkspaceID, entry, target, versions[key], writtenAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *V2ProjectionWriter) validateDurableSourceVersions(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	versions map[projectionSourceKey]int64,
) error {
	query := `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`
	if w.dialect == DialectPostgres {
		query = `SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
			WHERE workspace_id=$1 AND entity_kind=$2 AND entity_id=$3`
	}
	for key, incoming := range versions {
		var durable int64
		var deleted bool
		err := tx.QueryRowContext(ctx, query, workspaceID, key.kind, key.id).Scan(&durable, &deleted)
		if errors.Is(err, sql.ErrNoRows) {
			return projectionConflict(ProjectionWriteConflictMissingSourceVersion, workspaceID, projectionSourceReference(key), "durable source-version ledger row is missing")
		}
		if err != nil {
			return fmt.Errorf("read durable source version %s: %w", projectionSourceReference(key), err)
		}
		if deleted {
			return projectionConflict(ProjectionWriteConflictMappingVersion, workspaceID, projectionSourceReference(key), "durable source is deleted")
		}
		if durable != incoming {
			return projectionConflict(ProjectionWriteConflictMappingVersion, workspaceID, projectionSourceReference(key),
				fmt.Sprintf("durable=%d incoming=%d", durable, incoming))
		}
	}
	return nil
}

type projectionSourceKey struct {
	kind LegacyEntityKind
	id   string
}

type projectionMapTarget struct {
	kind string
	id   string
}

func validateProjectionWrite(input V2ProjectionWrite) (map[projectionSourceKey]int64, error) {
	workspaceID := input.WorkspaceID
	projectIDs := make(map[string]struct{}, len(input.Projection.Projects))
	for _, project := range input.Projection.Projects {
		if strings.TrimSpace(project.ID) == "" || strings.TrimSpace(project.Name) == "" {
			return nil, fmt.Errorf("%w: project identity and name are required", ErrInvalidProjectionWriteInput)
		}
		if _, duplicate := projectIDs[project.ID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "project/"+project.ID, "duplicate projection identity")
		}
		projectIDs[project.ID] = struct{}{}
	}
	roadmapIDs := make(map[string]V2LearningRoadmapProjection, len(input.Projection.Roadmaps))
	for _, roadmap := range input.Projection.Roadmaps {
		if strings.TrimSpace(roadmap.ID) == "" {
			return nil, fmt.Errorf("%w: roadmap identity is required", ErrInvalidProjectionWriteInput)
		}
		if _, ok := projectIDs[roadmap.ProjectID]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap/"+roadmap.ID, "project is absent from projection")
		}
		if _, duplicate := roadmapIDs[roadmap.ID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap/"+roadmap.ID, "duplicate projection identity")
		}
		roadmapIDs[roadmap.ID] = roadmap
	}
	orderedNodes, err := topologicallySortedV2RoadmapNodes(input.Projection.RoadmapNodes)
	if err != nil {
		return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap_nodes", err.Error())
	}
	roadmapNodeIDs := make(map[string]V2RoadmapNodeProjection, len(orderedNodes))
	for _, node := range orderedNodes {
		roadmap, ok := roadmapIDs[node.RoadmapID]
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.Title) == "" || !ok || roadmap.ProjectID != node.ProjectID {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap_node/"+node.ID, "roadmap/project is absent or mismatched")
		}
		roadmapNodeIDs[node.ID] = node
	}
	roadmapEdgeIDs := make(map[string]V2RoadmapEdgeProjection, len(input.Projection.RoadmapEdges))
	for _, edge := range input.Projection.RoadmapEdges {
		roadmap, roadmapOK := roadmapIDs[edge.RoadmapID]
		from, fromOK := roadmapNodeIDs[edge.FromNodeID]
		to, toOK := roadmapNodeIDs[edge.ToNodeID]
		if strings.TrimSpace(edge.ID) == "" || !roadmapOK || !fromOK || !toOK || roadmap.ProjectID != edge.ProjectID || from.RoadmapID != edge.RoadmapID || to.RoadmapID != edge.RoadmapID || edge.FromNodeID == edge.ToNodeID {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap_edge/"+edge.ID, "edge graph is absent or mismatched")
		}
		if _, duplicate := roadmapEdgeIDs[edge.ID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "roadmap_edge/"+edge.ID, "duplicate projection identity")
		}
		roadmapEdgeIDs[edge.ID] = edge
	}

	taskIDs := make(map[string]struct{}, len(input.Projection.Tasks))
	for _, task := range input.Projection.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Title) == "" {
			return nil, fmt.Errorf("%w: task identity and title are required", ErrInvalidProjectionWriteInput)
		}
		if _, ok := projectIDs[task.ProjectID]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "task/"+task.ID, "project is absent from projection")
		}
		if task.RoadmapNodeID != "" {
			node, ok := roadmapNodeIDs[task.RoadmapNodeID]
			if !ok || node.ProjectID != task.ProjectID {
				return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "task/"+task.ID, "roadmap node is absent or belongs to another project")
			}
		}
		if _, duplicate := taskIDs[task.ID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "task/"+task.ID, "duplicate projection identity")
		}
		taskIDs[task.ID] = struct{}{}
	}

	scheduleIDs := make(map[string]struct{}, len(input.Projection.Schedules))
	for _, schedule := range input.Projection.Schedules {
		if _, ok := taskIDs[schedule.TaskID]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "schedule/"+schedule.TaskID, "task is absent from projection")
		}
		if _, duplicate := scheduleIDs[schedule.TaskID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "schedule/"+schedule.TaskID, "duplicate projection identity")
		}
		scheduleIDs[schedule.TaskID] = struct{}{}
	}
	for taskID := range taskIDs {
		if _, ok := scheduleIDs[taskID]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "task/"+taskID, "schedule is absent from projection")
		}
	}

	occurrenceIDs := make(map[string]struct{}, len(input.Projection.Occurrences))
	for _, occurrence := range input.Projection.Occurrences {
		if strings.TrimSpace(occurrence.ID) == "" || strings.TrimSpace(occurrence.OccurrenceKey) == "" {
			return nil, fmt.Errorf("%w: occurrence identity and key are required", ErrInvalidProjectionWriteInput)
		}
		if _, ok := taskIDs[occurrence.TaskID]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "occurrence/"+occurrence.ID, "task is absent from projection")
		}
		if _, duplicate := occurrenceIDs[occurrence.ID]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "occurrence/"+occurrence.ID, "duplicate projection identity")
		}
		occurrenceIDs[occurrence.ID] = struct{}{}
	}

	expectedSources := make(map[projectionSourceKey]struct{}, len(input.Projection.IDMap))
	for _, entry := range input.Projection.IDMap {
		entry.LegacyID = strings.TrimSpace(entry.LegacyID)
		key := projectionSourceKey{kind: entry.LegacyKind, id: entry.LegacyID}
		if !validLegacyProjectionKind(entry.LegacyKind) || entry.LegacyID == "" {
			return nil, fmt.Errorf("%w: invalid ID-map source %s/%q", ErrInvalidProjectionWriteInput, entry.LegacyKind, entry.LegacyID)
		}
		if _, duplicate := expectedSources[key]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictMappingTarget, workspaceID, projectionSourceReference(key), "duplicate source mapping")
		}
		expectedSources[key] = struct{}{}
		targets := projectionMapTargets(entry)
		if len(targets) == 0 {
			return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, projectionSourceReference(key), "source has no canonical target")
		}
		for _, target := range targets {
			var exists bool
			switch target.kind {
			case "project":
				_, exists = projectIDs[target.id]
			case "roadmap":
				_, exists = roadmapIDs[target.id]
			case "roadmap_node":
				_, exists = roadmapNodeIDs[target.id]
			case "roadmap_edge":
				_, exists = roadmapEdgeIDs[target.id]
			case "task":
				_, exists = taskIDs[target.id]
			case "schedule":
				_, exists = scheduleIDs[target.id]
			case "occurrence":
				_, exists = occurrenceIDs[target.id]
			}
			if !exists {
				return nil, projectionConflict(ProjectionWriteConflictTargetData, workspaceID, projectionSourceReference(key), "mapped "+target.kind+" target is absent: "+target.id)
			}
		}
	}

	versions := make(map[projectionSourceKey]int64, len(input.SourceVersions))
	for _, source := range input.SourceVersions {
		key := projectionSourceKey{kind: source.EntityKind, id: strings.TrimSpace(source.LegacyID)}
		if !validLegacyProjectionKind(source.EntityKind) || key.id == "" || source.LogicalVersion <= 0 {
			return nil, projectionConflict(ProjectionWriteConflictInvalidSourceVersion, workspaceID, projectionSourceReference(key), fmt.Sprintf("logical_version=%d", source.LogicalVersion))
		}
		if _, duplicate := versions[key]; duplicate {
			return nil, projectionConflict(ProjectionWriteConflictInvalidSourceVersion, workspaceID, projectionSourceReference(key), "duplicate version entry")
		}
		versions[key] = source.LogicalVersion
	}
	for key := range expectedSources {
		if _, ok := versions[key]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictMissingSourceVersion, workspaceID, projectionSourceReference(key), "snapshot source version is required")
		}
	}
	for key := range versions {
		if _, ok := expectedSources[key]; !ok {
			return nil, projectionConflict(ProjectionWriteConflictUnmappedSourceVersion, workspaceID, projectionSourceReference(key), "source version has no ID map")
		}
	}
	return versions, nil
}

func validLegacyProjectionKind(kind LegacyEntityKind) bool {
	switch kind {
	case LegacyEntityProject, LegacyEntityTask, LegacyEntityRule, LegacyEntityOccurrence, LegacyEntityEvent,
		LegacyEntityRoadmap, LegacyEntityRoadmapNode, LegacyEntityRoadmapEdge:
		return true
	default:
		return false
	}
}

func projectionMapTargets(entry V2IDMapEntry) []projectionMapTarget {
	switch entry.LegacyKind {
	case LegacyEntityProject:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "project", id: entry.TargetProjectID})
	case LegacyEntityRoadmap:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "roadmap", id: entry.TargetRoadmapID})
	case LegacyEntityRoadmapNode:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "roadmap_node", id: entry.TargetRoadmapNodeID})
	case LegacyEntityRoadmapEdge:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "roadmap_edge", id: entry.TargetRoadmapEdgeID})
	case LegacyEntityTask:
		return nonemptyProjectionTargets(
			projectionMapTarget{kind: "task", id: entry.TargetTaskID},
			projectionMapTarget{kind: "schedule", id: entry.TargetScheduleID},
			projectionMapTarget{kind: "occurrence", id: entry.TargetOccurrenceID},
		)
	case LegacyEntityRule:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "schedule", id: entry.TargetScheduleID})
	case LegacyEntityOccurrence:
		return nonemptyProjectionTargets(projectionMapTarget{kind: "occurrence", id: entry.TargetOccurrenceID})
	case LegacyEntityEvent:
		return nonemptyProjectionTargets(
			projectionMapTarget{kind: "task", id: entry.TargetTaskID},
			projectionMapTarget{kind: "schedule", id: entry.TargetScheduleID},
			projectionMapTarget{kind: "occurrence", id: entry.TargetOccurrenceID},
		)
	default:
		return nil
	}
}

func nonemptyProjectionTargets(targets ...projectionMapTarget) []projectionMapTarget {
	result := make([]projectionMapTarget, 0, len(targets))
	for _, target := range targets {
		target.id = strings.TrimSpace(target.id)
		if target.id != "" {
			result = append(result, target)
		}
	}
	return result
}

func normalizeProjectedSchedule(schedule V2ScheduleProjection, occurrences []V2OccurrenceProjection) (V2ScheduleProjection, error) {
	if schedule.Interval <= 0 {
		schedule.Interval = 1
	}
	if schedule.TimingType != "time_block" || (schedule.LocalStartTime != "" && schedule.DurationMinutes > 0) {
		return schedule, nil
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return V2ScheduleProjection{}, fmt.Errorf("invalid timezone %q", schedule.Timezone)
	}
	for _, occurrence := range occurrences {
		if occurrence.TaskID != schedule.TaskID || occurrence.PlannedStartAt == nil || occurrence.PlannedEndAt == nil {
			continue
		}
		duration := occurrence.PlannedEndAt.Sub(*occurrence.PlannedStartAt)
		if duration <= 0 || duration%time.Minute != 0 || duration/time.Minute > math.MaxInt {
			return V2ScheduleProjection{}, errors.New("time-block occurrence duration must be a positive whole number of minutes")
		}
		schedule.LocalStartTime = occurrence.PlannedStartAt.In(location).Format("15:04:05")
		schedule.DurationMinutes = int(duration / time.Minute)
		return schedule, nil
	}
	return V2ScheduleProjection{}, errors.New("time-block schedule needs local time/duration or a matching planned occurrence")
}

func projectedRecurrenceRule(schedule V2ScheduleProjection) (string, error) {
	rule := make(map[string]any)
	switch schedule.RecurrenceType {
	case "none":
	case "daily":
		rule["interval"] = schedule.Interval
	case "weekly":
		rule["interval"] = schedule.Interval
		rule["weekdays"] = schedule.Weekdays
	case "monthly":
		rule["interval"] = schedule.Interval
		rule["month_days"] = schedule.MonthDays
	default:
		return "", fmt.Errorf("invalid recurrence type %q", schedule.RecurrenceType)
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (w *V2ProjectionWriter) writeProject(ctx context.Context, tx *sql.Tx, workspaceID string, project V2ProjectProjection, writtenAt string) error {
	args := []any{workspaceID, project.ID, project.Name, project.Kind, project.Horizon, "active", nullableProjectionString(project.SystemRole), writtenAt, writtenAt}
	query := `INSERT INTO domain_projects_v2
		(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,1,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_projects_v2 WHERE workspace_id=? AND id=? AND name IS ? AND kind IS ? AND horizon IS ? AND status='active' AND system_role IS ? AND revision=1)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_projects_v2
			(workspace_id,id,name,kind,horizon,status,system_role,revision,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,1,$8,$9) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_projects_v2 WHERE workspace_id=$1 AND id=$2 AND name IS NOT DISTINCT FROM $3 AND kind IS NOT DISTINCT FROM $4 AND horizon IS NOT DISTINCT FROM $5 AND status='active' AND system_role IS NOT DISTINCT FROM $6 AND revision=1)`
	}
	verifyArgs := []any{args[0], args[1], args[2], args[3], args[4], args[6]}
	return w.insertOrVerify(ctx, tx, query, args, verify, verifyArgs, workspaceID, "project/"+project.ID)
}

type roadmapNodeLegacyMetadata struct {
	LegacyNodeType       string   `json:"legacy_node_type"`
	PathType             string   `json:"path_type"`
	Deliverable          string   `json:"deliverable"`
	AcceptanceCriteria   string   `json:"acceptance_criteria"`
	CanvasX              float64  `json:"canvas_x"`
	CanvasY              float64  `json:"canvas_y"`
	ArticleSearchQueries []string `json:"article_search_queries"`
}

func roadmapNodeMetadataJSON(node V2RoadmapNodeProjection) (string, error) {
	encoded, err := json.Marshal(roadmapNodeLegacyMetadata{
		LegacyNodeType: node.LegacyNodeType, PathType: node.PathType,
		Deliverable: node.Deliverable, AcceptanceCriteria: node.AcceptanceCriteria,
		CanvasX: node.CanvasX, CanvasY: node.CanvasY,
		ArticleSearchQueries: append([]string(nil), node.ArticleSearchQueries...),
	})
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (w *V2ProjectionWriter) writeRoadmap(ctx context.Context, tx *sql.Tx, workspaceID string, roadmap V2LearningRoadmapProjection, writtenAt string) error {
	args := []any{workspaceID, roadmap.ID, roadmap.ProjectID, roadmap.Status, roadmap.Title, roadmap.Description, writtenAt, writtenAt}
	query := `INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,description,revision,created_at,updated_at)
		VALUES(?,?,?,?,?,?,1,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_learning_roadmaps_v2 WHERE workspace_id=? AND id=? AND project_id IS ? AND status IS ? AND title IS ? AND description IS ? AND revision=1)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,description,revision,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,1,$7,$8) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_learning_roadmaps_v2 WHERE workspace_id=$1 AND id=$2 AND project_id IS NOT DISTINCT FROM $3 AND status IS NOT DISTINCT FROM $4 AND title IS NOT DISTINCT FROM $5 AND description IS NOT DISTINCT FROM $6 AND revision=1)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, args[:6], workspaceID, "roadmap/"+roadmap.ID)
}

func (w *V2ProjectionWriter) writeRoadmapNode(ctx context.Context, tx *sql.Tx, workspaceID string, node V2RoadmapNodeProjection, writtenAt string) error {
	metadata, err := roadmapNodeMetadataJSON(node)
	if err != nil {
		return fmt.Errorf("encode roadmap-node/%s metadata: %w", node.ID, err)
	}
	args := []any{workspaceID, node.ID, node.ProjectID, node.RoadmapID, nullableProjectionString(node.ParentID), node.Title, node.Description, node.NodeType, node.Status, node.Position, metadata, writtenAt, writtenAt}
	query := `INSERT INTO domain_roadmap_nodes_v2(workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,status,position,legacy_metadata,revision,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=? AND project_id IS ? AND roadmap_id IS ? AND parent_id IS ? AND title IS ? AND description IS ? AND node_type IS ? AND status IS ? AND position IS ? AND legacy_metadata IS ? AND revision=1)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_roadmap_nodes_v2(workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,status,position,legacy_metadata,revision,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,1,$12,$13) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_roadmap_nodes_v2 WHERE workspace_id=$1 AND id=$2 AND project_id IS NOT DISTINCT FROM $3 AND roadmap_id IS NOT DISTINCT FROM $4 AND parent_id IS NOT DISTINCT FROM $5 AND title IS NOT DISTINCT FROM $6 AND description IS NOT DISTINCT FROM $7 AND node_type IS NOT DISTINCT FROM $8 AND status IS NOT DISTINCT FROM $9 AND position IS NOT DISTINCT FROM $10 AND legacy_metadata IS NOT DISTINCT FROM $11::jsonb AND revision=1)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, args[:11], workspaceID, "roadmap-node/"+node.ID)
}

func (w *V2ProjectionWriter) writeRoadmapEdge(ctx context.Context, tx *sql.Tx, workspaceID string, edge V2RoadmapEdgeProjection, writtenAt string) error {
	args := []any{workspaceID, edge.ID, edge.ProjectID, edge.RoadmapID, edge.FromNodeID, edge.ToNodeID, edge.EdgeType, writtenAt}
	query := `INSERT INTO domain_roadmap_edges_v2(workspace_id,id,project_id,roadmap_id,from_node_id,to_node_id,edge_type,revision,created_at)
		VALUES(?,?,?,?,?,?,?,1,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_roadmap_edges_v2 WHERE workspace_id=? AND id=? AND project_id IS ? AND roadmap_id IS ? AND from_node_id IS ? AND to_node_id IS ? AND edge_type IS ? AND revision=1)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_roadmap_edges_v2(workspace_id,id,project_id,roadmap_id,from_node_id,to_node_id,edge_type,revision,created_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,1,$8) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_roadmap_edges_v2 WHERE workspace_id=$1 AND id=$2 AND project_id IS NOT DISTINCT FROM $3 AND roadmap_id IS NOT DISTINCT FROM $4 AND from_node_id IS NOT DISTINCT FROM $5 AND to_node_id IS NOT DISTINCT FROM $6 AND edge_type IS NOT DISTINCT FROM $7 AND revision=1)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, args[:7], workspaceID, "roadmap-edge/"+edge.ID)
}

func (w *V2ProjectionWriter) writeTask(ctx context.Context, tx *sql.Tx, workspaceID string, task V2TaskProjection, writtenAt string) error {
	args := []any{workspaceID, task.ID, task.ProjectID, nullableProjectionString(task.RoadmapNodeID), nullableProjectionString(task.TaskNoteID), task.Title, task.Description, task.LifecycleStatus, task.Priority, task.SortOrder, writtenAt, writtenAt}
	query := `INSERT INTO domain_tasks_v2
		(workspace_id,id,project_id,roadmap_node_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,1,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_tasks_v2 WHERE workspace_id=? AND id=? AND project_id IS ? AND roadmap_node_id IS ? AND note_id IS ? AND title IS ? AND description IS ? AND lifecycle_status IS ? AND priority IS ? AND sort_order IS ? AND revision=1)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_tasks_v2
			(workspace_id,id,project_id,roadmap_node_id,note_id,title,description,lifecycle_status,priority,sort_order,revision,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_tasks_v2 WHERE workspace_id=$1 AND id=$2 AND project_id IS NOT DISTINCT FROM $3 AND roadmap_node_id IS NOT DISTINCT FROM $4 AND note_id IS NOT DISTINCT FROM $5 AND title IS NOT DISTINCT FROM $6 AND description IS NOT DISTINCT FROM $7 AND lifecycle_status IS NOT DISTINCT FROM $8 AND priority IS NOT DISTINCT FROM $9 AND sort_order IS NOT DISTINCT FROM $10 AND revision=1)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, args[:10], workspaceID, "task/"+task.ID)
}

func (w *V2ProjectionWriter) writeScheduleHeader(ctx context.Context, tx *sql.Tx, workspaceID string, schedule V2ScheduleProjection, writtenAt string) error {
	args := []any{workspaceID, schedule.TaskID, writtenAt}
	query := `INSERT INTO domain_task_schedules_v2
		(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
		VALUES (?,?,1,1,'idle',?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_task_schedules_v2 WHERE workspace_id=? AND task_id=? AND revision=1 AND current_schedule_revision=1 AND generation_status='idle' AND generation_error IS NULL AND generation_retry_at IS NULL AND generation_retry_pending_jobs=0 AND generation_failed_jobs=0)`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_task_schedules_v2
			(workspace_id,task_id,revision,current_schedule_revision,generation_status,updated_at)
			VALUES ($1,$2,1,1,'idle',$3) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_task_schedules_v2 WHERE workspace_id=$1 AND task_id=$2 AND revision=1 AND current_schedule_revision=1 AND generation_status='idle' AND generation_error IS NULL AND generation_retry_at IS NULL AND generation_retry_pending_jobs=0 AND generation_failed_jobs=0)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, args[:2], workspaceID, "schedule/"+schedule.TaskID)
}

func (w *V2ProjectionWriter) writeScheduleVersion(ctx context.Context, tx *sql.Tx, workspaceID string, schedule V2ScheduleProjection, writtenAt string) error {
	rule, err := projectedRecurrenceRule(schedule)
	if err != nil {
		return projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "schedule/"+schedule.TaskID, err.Error())
	}
	effectiveFrom := any(nil)
	if schedule.RecurrenceType != "none" {
		effectiveFrom = nullableProjectionString(schedule.StartsOn)
	}
	args := []any{workspaceID, schedule.TaskID, effectiveFrom, schedule.RecurrenceType, schedule.TimingType, schedule.Timezone,
		nullableProjectionString(schedule.StartsOn), nullableProjectionString(schedule.EndsOn), rule,
		nullableProjectionString(schedule.LocalStartTime), nullableProjectionPositiveInt(schedule.DurationMinutes), writtenAt}
	query := `INSERT INTO domain_task_schedule_versions_v2
		(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
		VALUES (?,?,1,?,NULL,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? AND schedule_revision=1 AND effective_from IS ? AND effective_to IS NULL AND recurrence_type IS ? AND timing_type IS ? AND timezone IS ? AND starts_on IS ? AND ends_on IS ? AND recurrence_rule IS ? AND local_start_time IS ? AND duration_minutes IS ?)`
	existsQuery := `SELECT EXISTS(SELECT 1 FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? AND schedule_revision=1)`
	verifyArgs := args[:11]
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_task_schedule_versions_v2
			(workspace_id,task_id,schedule_revision,effective_from,effective_to,recurrence_type,timing_type,timezone,starts_on,ends_on,recurrence_rule,local_start_time,duration_minutes,created_at)
			VALUES ($1,$2,1,$3,NULL,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$12) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_task_schedule_versions_v2 WHERE workspace_id=$1 AND task_id=$2 AND schedule_revision=1 AND effective_from IS NOT DISTINCT FROM $3 AND effective_to IS NULL AND recurrence_type IS NOT DISTINCT FROM $4 AND timing_type IS NOT DISTINCT FROM $5 AND timezone IS NOT DISTINCT FROM $6 AND starts_on IS NOT DISTINCT FROM $7 AND ends_on IS NOT DISTINCT FROM $8 AND recurrence_rule IS NOT DISTINCT FROM $9::jsonb AND local_start_time IS NOT DISTINCT FROM $10 AND duration_minutes IS NOT DISTINCT FROM $11)`
		existsQuery = `SELECT EXISTS(SELECT 1 FROM domain_task_schedule_versions_v2 WHERE workspace_id=$1 AND task_id=$2 AND schedule_revision=1)`
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, existsQuery, workspaceID, schedule.TaskID).Scan(&exists); err != nil {
		return fmt.Errorf("check existing schedule-version/%s/1 projection: %w", schedule.TaskID, err)
	}
	if exists {
		var matches bool
		if err := tx.QueryRowContext(ctx, verify, verifyArgs...).Scan(&matches); err != nil {
			return fmt.Errorf("verify existing schedule-version/%s/1 projection: %w", schedule.TaskID, err)
		}
		if !matches {
			return projectionConflict(ProjectionWriteConflictTargetData, workspaceID, "schedule-version/"+schedule.TaskID+"/1", "existing target differs from snapshot")
		}
		return nil
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, verifyArgs, workspaceID, "schedule-version/"+schedule.TaskID+"/1")
}

func (w *V2ProjectionWriter) writeOccurrence(ctx context.Context, tx *sql.Tx, workspaceID string, occurrence V2OccurrenceProjection, writtenAt string) error {
	args := []any{workspaceID, occurrence.ID, occurrence.TaskID, occurrence.OccurrenceKey,
		nullableProjectionString(occurrence.PlannedDate), projectionTime(occurrence.PlannedStartAt), projectionTime(occurrence.PlannedEndAt),
		projectionTime(occurrence.DueAt), occurrence.ExecutionStatus, projectionTime(occurrence.CompletedAt),
		nullableProjectionString(occurrence.Location), nullableProjectionString(occurrence.CalendarKind), nullableProjectionString(occurrence.CalendarNotes),
		nullableProjectionString(occurrence.OccurrenceNoteID), nullableProjectionString(occurrence.AllDayEndDate),
		nullableProjectionString(occurrence.BlockedReason), nullableProjectionString(occurrence.NextAction), occurrence.GeneratedScheduleRevision,
		writtenAt, writtenAt}
	query := `INSERT INTO domain_task_occurrences_v2
		(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,completed_at,
		 location,calendar_kind,calendar_notes,note_id,all_day_end_date,blocked_reason,next_action,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,0,?,?) ON CONFLICT DO NOTHING`
	verify := `SELECT EXISTS(SELECT 1 FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=? AND task_id IS ? AND occurrence_key IS ?
		AND planned_date IS ? AND planned_start_at IS ? AND planned_end_at IS ? AND due_at IS ? AND execution_status IS ? AND completed_at IS ?
		AND location IS ? AND calendar_kind IS ? AND calendar_notes IS ? AND note_id IS ? AND all_day_end_date IS ?
		AND blocked_reason IS ? AND next_action IS ? AND revision=1 AND generated_schedule_revision IS ? AND manually_overridden=0)`
	verifyArgs := args[:18]
	if w.dialect == DialectPostgres {
		query = `INSERT INTO domain_task_occurrences_v2
			(workspace_id,id,task_id,occurrence_key,planned_date,planned_start_at,planned_end_at,due_at,execution_status,completed_at,
			 location,calendar_kind,calendar_notes,note_id,all_day_end_date,blocked_reason,next_action,revision,generated_schedule_revision,manually_overridden,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,1,$18,FALSE,$19,$20) ON CONFLICT DO NOTHING`
		verify = `SELECT EXISTS(SELECT 1 FROM domain_task_occurrences_v2 WHERE workspace_id=$1 AND id=$2 AND task_id IS NOT DISTINCT FROM $3 AND occurrence_key IS NOT DISTINCT FROM $4
			AND planned_date IS NOT DISTINCT FROM $5 AND planned_start_at IS NOT DISTINCT FROM $6 AND planned_end_at IS NOT DISTINCT FROM $7 AND due_at IS NOT DISTINCT FROM $8 AND execution_status IS NOT DISTINCT FROM $9 AND completed_at IS NOT DISTINCT FROM $10
			AND location IS NOT DISTINCT FROM $11 AND calendar_kind IS NOT DISTINCT FROM $12 AND calendar_notes IS NOT DISTINCT FROM $13 AND note_id IS NOT DISTINCT FROM $14 AND all_day_end_date IS NOT DISTINCT FROM $15
			AND blocked_reason IS NOT DISTINCT FROM $16 AND next_action IS NOT DISTINCT FROM $17 AND revision=1 AND generated_schedule_revision IS NOT DISTINCT FROM $18 AND manually_overridden=FALSE)`
	}
	return w.insertOrVerify(ctx, tx, query, args, verify, verifyArgs, workspaceID, "occurrence/"+occurrence.ID)
}

func (w *V2ProjectionWriter) writeIDMap(ctx context.Context, tx *sql.Tx, workspaceID string, entry V2IDMapEntry, target projectionMapTarget, version int64, writtenAt string) error {
	args := []any{workspaceID, entry.LegacyKind, entry.LegacyID, target.kind, target.id, version, writtenAt}
	query := `INSERT INTO task_domain_legacy_id_map
		(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
		VALUES (?,?,?,?,?,?,0,?) ON CONFLICT DO NOTHING`
	if w.dialect == DialectPostgres {
		query = `INSERT INTO task_domain_legacy_id_map
			(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,FALSE,$7) ON CONFLICT DO NOTHING`
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("insert task domain ID map %s/%s -> %s/%s: %w", entry.LegacyKind, entry.LegacyID, target.kind, target.id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read task domain ID map insert result: %w", err)
	}
	if rows == 1 {
		return nil
	}

	queryExisting := `SELECT v2_id,source_logical_version,deleted FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=? AND target_kind=?`
	if w.dialect == DialectPostgres {
		queryExisting = `SELECT v2_id,source_logical_version,deleted FROM task_domain_legacy_id_map
			WHERE workspace_id=$1 AND entity_kind=$2 AND legacy_id=$3 AND target_kind=$4`
	}
	var existingID string
	var existingVersion int64
	var deleted bool
	err = tx.QueryRowContext(ctx, queryExisting, workspaceID, entry.LegacyKind, entry.LegacyID, target.kind).Scan(&existingID, &existingVersion, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return projectionConflict(ProjectionWriteConflictMappingTarget, workspaceID, string(entry.LegacyKind)+"/"+entry.LegacyID, "target is already owned by another source")
	}
	if err != nil {
		return fmt.Errorf("read existing task domain ID map: %w", err)
	}
	if existingVersion != version {
		return projectionConflict(ProjectionWriteConflictMappingVersion, workspaceID, string(entry.LegacyKind)+"/"+entry.LegacyID,
			fmt.Sprintf("existing=%d incoming=%d", existingVersion, version))
	}
	if deleted || existingID != target.id {
		return projectionConflict(ProjectionWriteConflictMappingTarget, workspaceID, string(entry.LegacyKind)+"/"+entry.LegacyID,
			fmt.Sprintf("existing=%s deleted=%t incoming=%s", existingID, deleted, target.id))
	}
	return nil
}

func (w *V2ProjectionWriter) insertOrVerify(
	ctx context.Context,
	tx *sql.Tx,
	insertSQL string,
	insertArgs []any,
	verifySQL string,
	verifyArgs []any,
	workspaceID string,
	reference string,
) error {
	result, err := tx.ExecContext(ctx, insertSQL, insertArgs...)
	if err != nil {
		return fmt.Errorf("insert %s projection: %w", reference, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s projection insert result: %w", reference, err)
	}
	if rows == 1 {
		return nil
	}
	var matches bool
	if err := tx.QueryRowContext(ctx, verifySQL, verifyArgs...).Scan(&matches); err != nil {
		return fmt.Errorf("verify existing %s projection: %w", reference, err)
	}
	if !matches {
		return projectionConflict(ProjectionWriteConflictTargetData, workspaceID, reference, "existing target differs from snapshot")
	}
	return nil
}

func nullableProjectionString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableProjectionPositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

func projectionTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func projectionSourceReference(key projectionSourceKey) string {
	return string(key.kind) + "/" + key.id
}

func projectionConflict(code ProjectionWriteConflictCode, workspaceID, reference, detail string) error {
	return &ProjectionWriteConflictError{Code: code, WorkspaceID: workspaceID, Reference: reference, Detail: detail}
}
