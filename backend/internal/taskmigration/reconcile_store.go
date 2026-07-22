package taskmigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidReconcileStoreInput = errors.New("invalid reconcile store input")

type ReconcileApplyBlockCode string

const (
	ReconcileApplyUnsafeMismatch   ReconcileApplyBlockCode = "unsafe_mismatch"
	ReconcileApplyStaleObservation ReconcileApplyBlockCode = "stale_observation"
	ReconcileApplySourceNotDeleted ReconcileApplyBlockCode = "source_not_deleted"
	ReconcileApplyGeneratedTarget  ReconcileApplyBlockCode = "generated_target"
	ReconcileApplyMissingTarget    ReconcileApplyBlockCode = "missing_target"
)

type ReconcileApplyBlock struct {
	Code      ReconcileApplyBlockCode
	Reference string
	Detail    string
}

func (e *ReconcileApplyBlock) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reference)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Reference, e.Detail)
}

type ReconcileObservation struct {
	WorkspaceID    string
	Projection     V2Projection
	SourceVersions []ProjectionSourceVersion
	Input          ReconcileInput
	Plan           ReconcilePlan
}

type ReconcileApplyResult struct {
	// Ready is deliberately always false. A successful repair has to be
	// followed by a new read-only observation before a coordinator may enter
	// ready/cutover.
	Ready               bool
	RequiresObservation bool
}

type ReconcileStore struct {
	db      *sql.DB
	dialect Dialect
	writer  *V2ProjectionWriter
}

func NewReconcileStore(db *sql.DB, dialect Dialect) (*ReconcileStore, error) {
	if db == nil || (dialect != DialectSQLite && dialect != DialectPostgres) {
		return nil, ErrInvalidReconcileStoreInput
	}
	writer, err := NewV2ProjectionWriter(dialect)
	if err != nil {
		return nil, err
	}
	return &ReconcileStore{db: db, dialect: dialect, writer: writer}, nil
}

// Observe derives the canonical source inventory from the frozen snapshot
// projection and reads every durable target, ID map and invariant in one
// read-only transaction. The returned plan is only an observation; it does
// not mutate migration state and cannot by itself authorize cutover.
func (s *ReconcileStore) Observe(
	ctx context.Context,
	workspaceID string,
	projection V2Projection,
	sourceVersions []ProjectionSourceVersion,
) (ReconcileObservation, error) {
	if s == nil || s.db == nil || ctx == nil || strings.TrimSpace(workspaceID) == "" {
		return ReconcileObservation{}, ErrInvalidReconcileStoreInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return ReconcileObservation{}, fmt.Errorf("begin reconcile observation: %w", err)
	}
	defer tx.Rollback()
	observation, err := s.observeTx(ctx, tx, strings.TrimSpace(workspaceID), projection, sourceVersions)
	if err != nil {
		return ReconcileObservation{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReconcileObservation{}, fmt.Errorf("commit reconcile observation: %w", err)
	}
	return observation, nil
}

// ApplyPlan performs only the two mechanically safe repair classes:
// re-persisting an exact frozen projection and deleting mapped targets whose
// durable legacy ledger is tombstoned. It re-observes under the write
// transaction to reject stale caller plans and never returns Ready.
func (s *ReconcileStore) ApplyPlan(
	ctx context.Context,
	observed ReconcileObservation,
	writtenAt time.Time,
) (ReconcileApplyResult, error) {
	if s == nil || s.db == nil || ctx == nil || strings.TrimSpace(observed.WorkspaceID) == "" || writtenAt.IsZero() {
		return ReconcileApplyResult{}, ErrInvalidReconcileStoreInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ReconcileApplyResult{}, fmt.Errorf("begin reconcile repair: %w", err)
	}
	defer tx.Rollback()

	current, err := s.observeTx(ctx, tx, observed.WorkspaceID, observed.Projection, observed.SourceVersions)
	if err != nil {
		return ReconcileApplyResult{}, err
	}
	if !reflect.DeepEqual(current.Input, observed.Input) || !reflect.DeepEqual(current.Plan, observed.Plan) {
		return ReconcileApplyResult{}, &ReconcileApplyBlock{Code: ReconcileApplyStaleObservation, Reference: observed.WorkspaceID}
	}
	if mismatch := firstUnsafeReconcileMismatch(current.Plan.Mismatches); mismatch != nil {
		return ReconcileApplyResult{}, &ReconcileApplyBlock{
			Code: ReconcileApplyUnsafeMismatch, Reference: replayKeyReference(mismatch.Entity), Detail: string(mismatch.Code),
		}
	}

	if len(current.Plan.UpsertMissing) != 0 {
		if err := s.writer.Write(ctx, tx, V2ProjectionWrite{
			WorkspaceID: current.WorkspaceID, Projection: current.Projection,
			SourceVersions: current.SourceVersions, WrittenAt: writtenAt,
		}); err != nil {
			return ReconcileApplyResult{}, err
		}
	}
	for _, mutation := range current.Plan.DeleteExtra {
		if err := s.deleteMappedTarget(ctx, tx, current.WorkspaceID, mutation, writtenAt); err != nil {
			return ReconcileApplyResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ReconcileApplyResult{}, fmt.Errorf("commit reconcile repair: %w", err)
	}
	return ReconcileApplyResult{Ready: false, RequiresObservation: true}, nil
}

func (s *ReconcileStore) observeTx(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projection V2Projection,
	sourceVersions []ProjectionSourceVersion,
) (ReconcileObservation, error) {
	versionMap, err := validateProjectionWrite(V2ProjectionWrite{
		WorkspaceID: workspaceID, Projection: projection,
		SourceVersions: sourceVersions, WrittenAt: time.Unix(1, 0).UTC(),
	})
	if err != nil {
		return ReconcileObservation{}, err
	}
	if err := s.writer.validateDurableSourceVersions(ctx, tx, workspaceID, versionMap); err != nil {
		return ReconcileObservation{}, err
	}

	expectedDigests, generated, err := expectedProjectionDigests(projection)
	if err != nil {
		return ReconcileObservation{}, err
	}
	sourceRows, err := canonicalSourceRows(projection.IDMap, expectedDigests)
	if err != nil {
		return ReconcileObservation{}, err
	}
	v2Rows, actualGenerated, fkViolations, statusViolations, err := s.readV2Rows(ctx, tx, workspaceID)
	if err != nil {
		return ReconcileObservation{}, err
	}
	generated = append(generated, actualGenerated...)
	generated = uniqueReplayKeys(generated)
	maps, err := s.readIDMaps(ctx, tx, workspaceID, v2Rows)
	if err != nil {
		return ReconcileObservation{}, err
	}
	input := ReconcileInput{
		Source: sourceRows, V2: v2Rows, IDMaps: maps, GeneratedSystemIDs: generated,
		ForeignKeyViolations: fkViolations, StatusViolations: statusViolations,
	}
	plan, err := ReconcileInventories(input)
	if err != nil {
		return ReconcileObservation{}, err
	}
	return ReconcileObservation{
		WorkspaceID: workspaceID, Projection: projection,
		SourceVersions: append([]ProjectionSourceVersion(nil), sourceVersions...),
		Input:          input, Plan: plan,
	}, nil
}

func firstUnsafeReconcileMismatch(mismatches []ReconcileMismatch) *ReconcileMismatch {
	for i := range mismatches {
		switch mismatches[i].Code {
		case ReconcileMismatchRowCount, ReconcileMismatchChecksum:
			// These are expected aggregate consequences of a concrete missing
			// or extra row and are cleared by the corresponding safe repair.
		default:
			return &mismatches[i]
		}
	}
	return nil
}

func expectedProjectionDigests(projection V2Projection) (map[ReplayEntityKey]string, []ReplayEntityKey, error) {
	result := make(map[ReplayEntityKey]string)
	generated := make([]ReplayEntityKey, 0)
	for _, project := range projection.Projects {
		key := reconcileStoreKey(ReplayEntityProject, project.ID)
		digest, err := digestCanonical(projectDigestRow{
			ID: project.ID, Name: project.Name, Kind: project.Kind, Horizon: project.Horizon,
			Status: "active", SystemRole: project.SystemRole,
		})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
		if project.Generated || project.SystemRole != "" {
			generated = append(generated, key)
		}
	}
	for _, roadmap := range projection.Roadmaps {
		key := reconcileStoreKey(ReplayEntityRoadmap, roadmap.ID)
		digest, err := digestCanonical(roadmapDigestRow{ID: roadmap.ID, ProjectID: roadmap.ProjectID, Status: roadmap.Status, Title: roadmap.Title, Description: roadmap.Description})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	for _, node := range projection.RoadmapNodes {
		metadata, err := roadmapNodeMetadataJSON(node)
		if err == nil {
			metadata, err = canonicalJSONString(metadata)
		}
		if err != nil {
			return nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityRoadmapNode, node.ID)
		digest, err := digestCanonical(roadmapNodeDigestRow{ID: node.ID, ProjectID: node.ProjectID, RoadmapID: node.RoadmapID, ParentID: node.ParentID, Title: node.Title, Description: node.Description, NodeType: node.NodeType, Status: node.Status, Position: node.Position, LegacyMetadata: metadata})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	for _, edge := range projection.RoadmapEdges {
		key := reconcileStoreKey(ReplayEntityRoadmapEdge, edge.ID)
		digest, err := digestCanonical(roadmapEdgeDigestRow{ID: edge.ID, ProjectID: edge.ProjectID, RoadmapID: edge.RoadmapID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID, EdgeType: edge.EdgeType})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	for _, task := range projection.Tasks {
		key := reconcileStoreKey(ReplayEntityTask, task.ID)
		digest, err := digestCanonical(taskDigestRow{
			ID: task.ID, ProjectID: task.ProjectID, RoadmapNodeID: task.RoadmapNodeID, NoteID: task.TaskNoteID, Title: task.Title,
			Description: task.Description, LifecycleStatus: string(task.LifecycleStatus), Priority: task.Priority, SortOrder: float64(task.SortOrder),
		})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	for _, schedule := range projection.Schedules {
		normalized, err := normalizeProjectedSchedule(schedule, projection.Occurrences)
		if err != nil {
			return nil, nil, err
		}
		rule, err := projectedRecurrenceRule(normalized)
		if err != nil {
			return nil, nil, err
		}
		rule, err = canonicalJSONString(rule)
		if err != nil {
			return nil, nil, err
		}
		effectiveFrom := ""
		if normalized.RecurrenceType != "none" {
			effectiveFrom = normalized.StartsOn
		}
		key := reconcileStoreKey(ReplayEntitySchedule, normalized.TaskID)
		digest, err := digestCanonical(scheduleDigestRow{
			TaskID: normalized.TaskID, CurrentRevision: 1, GenerationStatus: "idle",
			ScheduleRevision: 1, EffectiveFrom: effectiveFrom, RecurrenceType: string(normalized.RecurrenceType),
			TimingType: string(normalized.TimingType), Timezone: normalized.Timezone, StartsOn: normalized.StartsOn,
			EndsOn: normalized.EndsOn, RecurrenceRule: rule, LocalStartTime: normalized.LocalStartTime,
			DurationMinutes: normalized.DurationMinutes,
		})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	for _, occurrence := range projection.Occurrences {
		key := reconcileStoreKey(ReplayEntityOccurrence, occurrence.ID)
		digest, err := digestCanonical(occurrenceDigestRow{
			ID: occurrence.ID, TaskID: occurrence.TaskID, OccurrenceKey: occurrence.OccurrenceKey,
			PlannedDate: occurrence.PlannedDate, PlannedStartAt: canonicalExpectedTime(occurrence.PlannedStartAt),
			PlannedEndAt: canonicalExpectedTime(occurrence.PlannedEndAt), DueAt: canonicalExpectedTime(occurrence.DueAt),
			ExecutionStatus: string(occurrence.ExecutionStatus), CompletedAt: canonicalExpectedTime(occurrence.CompletedAt),
			Location: occurrence.Location, CalendarKind: occurrence.CalendarKind, CalendarNotes: occurrence.CalendarNotes,
			NoteID: occurrence.OccurrenceNoteID, AllDayEndDate: occurrence.AllDayEndDate,
			BlockedReason: occurrence.BlockedReason, NextAction: occurrence.NextAction,
			GeneratedScheduleRevision: occurrence.GeneratedScheduleRevision,
		})
		if err != nil {
			return nil, nil, err
		}
		if err := putProjectionDigest(result, key, digest); err != nil {
			return nil, nil, err
		}
	}
	return result, generated, nil
}

func canonicalSourceRows(idMaps []V2IDMapEntry, digests map[ReplayEntityKey]string) ([]CanonicalSourceRow, error) {
	rows := make([]CanonicalSourceRow, 0)
	for _, entry := range idMaps {
		source := reconcileStoreKey(ReplayEntityKind(entry.LegacyKind), strings.TrimSpace(entry.LegacyID))
		for _, target := range projectionMapTargets(entry) {
			key := reconcileStoreKey(reconcileTargetKind(target.kind), target.id)
			digest, ok := digests[key]
			if !ok {
				return nil, &ReconcileBlock{Code: ReconcileBlockInvalidIdentity, Reference: replayKeyReference(key), Detail: "ID map target is absent from frozen projection"}
			}
			rows = append(rows, CanonicalSourceRow{Source: source, Target: key, Digest: digest})
		}
	}
	return rows, nil
}

func (s *ReconcileStore) readV2Rows(ctx context.Context, tx *sql.Tx, workspaceID string) (
	[]V2MappedRow, []ReplayEntityKey, []ReconcileViolation, []ReconcileViolation, error,
) {
	var rows []V2MappedRow
	var generated []ReplayEntityKey
	var fkViolations []ReconcileViolation
	var statusViolations []ReconcileViolation

	projectRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT id,name,kind,horizon,status,system_role FROM domain_projects_v2 WHERE workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile projects: %w", err)
	}
	for projectRows.Next() {
		var id, name, kind, horizon, status string
		var systemRole sql.NullString
		if err := projectRows.Scan(&id, &name, &kind, &horizon, &status, &systemRole); err != nil {
			projectRows.Close()
			return nil, nil, nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityProject, id)
		digest, _ := digestCanonical(projectDigestRow{ID: id, Name: name, Kind: kind, Horizon: horizon, Status: status, SystemRole: systemRole.String})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if systemRole.Valid {
			generated = append(generated, key)
		}
		if !oneOf(status, "planning", "active", "paused", "completed", "archived") || !oneOf(kind, "standard", "learning") || !oneOf(horizon, "short", "long") {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid project enum state"})
		}
	}
	if err := projectRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	roadmapRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT r.id,r.project_id,r.status,r.title,r.description,
		CASE WHEN p.id IS NULL THEN 1 ELSE 0 END FROM domain_learning_roadmaps_v2 r
		LEFT JOIN domain_projects_v2 p ON p.workspace_id=r.workspace_id AND p.id=r.project_id
		WHERE r.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile roadmaps: %w", err)
	}
	for roadmapRows.Next() {
		var id, projectID, status, title, description string
		var missingProject int
		if err := roadmapRows.Scan(&id, &projectID, &status, &title, &description, &missingProject); err != nil {
			roadmapRows.Close()
			return nil, nil, nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityRoadmap, id)
		digest, _ := digestCanonical(roadmapDigestRow{ID: id, ProjectID: projectID, Status: status, Title: title, Description: description})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingProject != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "project is missing"})
		}
		if !oneOf(status, "draft", "active", "completed", "failed", "archived") {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid roadmap status"})
		}
	}
	if err := roadmapRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	nodeRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT n.id,n.project_id,n.roadmap_id,n.parent_id,n.title,n.description,n.node_type,n.status,n.position,n.legacy_metadata,
		CASE WHEN r.id IS NULL THEN 1 ELSE 0 END,CASE WHEN n.parent_id IS NOT NULL AND parent.id IS NULL THEN 1 ELSE 0 END
		FROM domain_roadmap_nodes_v2 n
		LEFT JOIN domain_learning_roadmaps_v2 r ON r.workspace_id=n.workspace_id AND r.project_id=n.project_id AND r.id=n.roadmap_id
		LEFT JOIN domain_roadmap_nodes_v2 parent ON parent.workspace_id=n.workspace_id AND parent.project_id=n.project_id AND parent.roadmap_id=n.roadmap_id AND parent.id=n.parent_id
		WHERE n.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile roadmap nodes: %w", err)
	}
	for nodeRows.Next() {
		var id, projectID, roadmapID, title, description, nodeType, status string
		var parentID sql.NullString
		var position float64
		var metadataValue any
		var missingRoadmap, missingParent int
		if err := nodeRows.Scan(&id, &projectID, &roadmapID, &parentID, &title, &description, &nodeType, &status, &position, &metadataValue, &missingRoadmap, &missingParent); err != nil {
			nodeRows.Close()
			return nil, nil, nil, nil, err
		}
		metadata, jsonErr := canonicalJSONString(canonicalString(metadataValue))
		key := reconcileStoreKey(ReplayEntityRoadmapNode, id)
		if jsonErr != nil {
			metadata = canonicalString(metadataValue)
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid legacy_metadata JSON"})
		}
		digest, _ := digestCanonical(roadmapNodeDigestRow{ID: id, ProjectID: projectID, RoadmapID: roadmapID, ParentID: parentID.String, Title: title, Description: description, NodeType: nodeType, Status: status, Position: position, LegacyMetadata: metadata})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingRoadmap != 0 || missingParent != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "roadmap or parent is missing"})
		}
		if !oneOf(nodeType, "stage", "topic", "milestone") || !oneOf(status, "locked", "available", "in_progress", "mastered", "skipped") {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid roadmap node state"})
		}
	}
	if err := nodeRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	edgeRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT e.id,e.project_id,e.roadmap_id,e.from_node_id,e.to_node_id,e.edge_type,
		CASE WHEN r.id IS NULL OR f.id IS NULL OR t.id IS NULL THEN 1 ELSE 0 END
		FROM domain_roadmap_edges_v2 e
		LEFT JOIN domain_learning_roadmaps_v2 r ON r.workspace_id=e.workspace_id AND r.project_id=e.project_id AND r.id=e.roadmap_id
		LEFT JOIN domain_roadmap_nodes_v2 f ON f.workspace_id=e.workspace_id AND f.project_id=e.project_id AND f.roadmap_id=e.roadmap_id AND f.id=e.from_node_id
		LEFT JOIN domain_roadmap_nodes_v2 t ON t.workspace_id=e.workspace_id AND t.project_id=e.project_id AND t.roadmap_id=e.roadmap_id AND t.id=e.to_node_id
		WHERE e.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile roadmap edges: %w", err)
	}
	for edgeRows.Next() {
		var id, projectID, roadmapID, fromID, toID, edgeType string
		var missingGraph int
		if err := edgeRows.Scan(&id, &projectID, &roadmapID, &fromID, &toID, &edgeType, &missingGraph); err != nil {
			edgeRows.Close()
			return nil, nil, nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityRoadmapEdge, id)
		digest, _ := digestCanonical(roadmapEdgeDigestRow{ID: id, ProjectID: projectID, RoadmapID: roadmapID, FromNodeID: fromID, ToNodeID: toID, EdgeType: edgeType})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingGraph != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "roadmap graph target is missing"})
		}
		if !oneOf(edgeType, "prerequisite", "related", "suggested_order") {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid roadmap edge type"})
		}
	}
	if err := edgeRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	taskRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT t.id,t.project_id,t.roadmap_node_id,t.note_id,t.title,t.description,t.lifecycle_status,t.priority,t.sort_order,
		CASE WHEN p.id IS NULL THEN 1 ELSE 0 END,CASE WHEN t.roadmap_node_id IS NOT NULL AND n.id IS NULL THEN 1 ELSE 0 END
		FROM domain_tasks_v2 t LEFT JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		LEFT JOIN domain_roadmap_nodes_v2 n ON n.workspace_id=t.workspace_id AND n.project_id=t.project_id AND n.id=t.roadmap_node_id
		WHERE t.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile tasks: %w", err)
	}
	for taskRows.Next() {
		var id, projectID, title, description, lifecycle string
		var roadmapNodeID, noteID sql.NullString
		var priority int
		var sortOrder float64
		var missingProject, missingRoadmapNode int
		if err := taskRows.Scan(&id, &projectID, &roadmapNodeID, &noteID, &title, &description, &lifecycle, &priority, &sortOrder, &missingProject, &missingRoadmapNode); err != nil {
			taskRows.Close()
			return nil, nil, nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityTask, id)
		digest, _ := digestCanonical(taskDigestRow{ID: id, ProjectID: projectID, RoadmapNodeID: roadmapNodeID.String, NoteID: noteID.String, Title: title, Description: description, LifecycleStatus: lifecycle, Priority: priority, SortOrder: sortOrder})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingProject != 0 || missingRoadmapNode != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "project or roadmap node is missing"})
		}
		if !oneOf(lifecycle, "draft", "active", "paused", "completed", "cancelled", "archived") || priority < 0 || priority > 3 {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid task status or priority"})
		}
	}
	if err := taskRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	scheduleRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT s.task_id,s.current_schedule_revision,s.generation_status,
		v.schedule_revision,v.effective_from,v.effective_to,v.recurrence_type,v.timing_type,v.timezone,v.starts_on,v.ends_on,
		v.recurrence_rule,v.local_start_time,v.duration_minutes,
		CASE WHEN t.id IS NULL THEN 1 ELSE 0 END,CASE WHEN v.task_id IS NULL THEN 1 ELSE 0 END
		FROM domain_task_schedules_v2 s
		LEFT JOIN domain_tasks_v2 t ON t.workspace_id=s.workspace_id AND t.id=s.task_id
		LEFT JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=s.workspace_id AND v.task_id=s.task_id AND v.schedule_revision=s.current_schedule_revision
		WHERE s.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile schedules: %w", err)
	}
	for scheduleRows.Next() {
		var values [14]any
		var missingTask, missingVersion int
		pointers := make([]any, 0, 16)
		for i := range values {
			pointers = append(pointers, &values[i])
		}
		pointers = append(pointers, &missingTask, &missingVersion)
		if err := scheduleRows.Scan(pointers...); err != nil {
			scheduleRows.Close()
			return nil, nil, nil, nil, err
		}
		taskID := canonicalString(values[0])
		key := reconcileStoreKey(ReplayEntitySchedule, taskID)
		rule, jsonErr := canonicalJSONString(canonicalString(values[11]))
		if jsonErr != nil {
			rule = canonicalString(values[11])
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid recurrence_rule JSON"})
		}
		digest, _ := digestCanonical(scheduleDigestRow{
			TaskID: taskID, CurrentRevision: canonicalInt64(values[1]), GenerationStatus: canonicalString(values[2]),
			ScheduleRevision: canonicalInt64(values[3]), EffectiveFrom: canonicalString(values[4]), EffectiveTo: canonicalString(values[5]),
			RecurrenceType: canonicalString(values[6]), TimingType: canonicalString(values[7]), Timezone: canonicalString(values[8]),
			StartsOn: canonicalString(values[9]), EndsOn: canonicalString(values[10]), RecurrenceRule: rule,
			LocalStartTime: canonicalTimeOfDay(values[12]), DurationMinutes: int(canonicalInt64(values[13])),
		})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingTask != 0 || missingVersion != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "task or current schedule version is missing"})
		}
		if !validObservedSchedule(values) {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid schedule state"})
		}
	}
	if err := scheduleRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	occurrenceRows, err := tx.QueryContext(ctx, s.workspaceQuery(`SELECT o.id,o.task_id,o.occurrence_key,o.planned_date,o.planned_start_at,o.planned_end_at,o.due_at,
		o.execution_status,o.completed_at,o.location,o.calendar_kind,o.calendar_notes,o.note_id,o.all_day_end_date,o.blocked_reason,o.next_action,
		o.generated_schedule_revision,CASE WHEN t.id IS NULL THEN 1 ELSE 0 END,CASE WHEN v.task_id IS NULL THEN 1 ELSE 0 END
		FROM domain_task_occurrences_v2 o
		LEFT JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
		LEFT JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=o.workspace_id AND v.task_id=o.task_id AND v.schedule_revision=o.generated_schedule_revision
		WHERE o.workspace_id=?`), workspaceID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("read reconcile occurrences: %w", err)
	}
	for occurrenceRows.Next() {
		var values [17]any
		var missingTask, missingVersion int
		pointers := make([]any, 0, 19)
		for i := range values {
			pointers = append(pointers, &values[i])
		}
		pointers = append(pointers, &missingTask, &missingVersion)
		if err := occurrenceRows.Scan(pointers...); err != nil {
			occurrenceRows.Close()
			return nil, nil, nil, nil, err
		}
		key := reconcileStoreKey(ReplayEntityOccurrence, canonicalString(values[0]))
		digest, _ := digestCanonical(occurrenceDigestRow{
			ID: canonicalString(values[0]), TaskID: canonicalString(values[1]), OccurrenceKey: canonicalString(values[2]),
			PlannedDate: canonicalString(values[3]), PlannedStartAt: canonicalInstant(values[4]), PlannedEndAt: canonicalInstant(values[5]),
			DueAt: canonicalInstant(values[6]), ExecutionStatus: canonicalString(values[7]), CompletedAt: canonicalInstant(values[8]),
			Location: canonicalString(values[9]), CalendarKind: canonicalString(values[10]), CalendarNotes: canonicalString(values[11]),
			NoteID: canonicalString(values[12]), AllDayEndDate: canonicalString(values[13]), BlockedReason: canonicalString(values[14]),
			NextAction: canonicalString(values[15]), GeneratedScheduleRevision: canonicalInt64(values[16]),
		})
		rows = append(rows, V2MappedRow{Target: key, Digest: digest})
		if missingTask != 0 || missingVersion != 0 {
			fkViolations = append(fkViolations, ReconcileViolation{Entity: key, Detail: "task or generating schedule version is missing"})
		}
		if !validObservedOccurrence(values) {
			statusViolations = append(statusViolations, ReconcileViolation{Entity: key, Detail: "invalid occurrence execution state"})
		}
	}
	if err := occurrenceRows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}

	sort.Slice(rows, func(i, j int) bool { return lessReplayKey(rows[i].Target, rows[j].Target) })
	return rows, generated, fkViolations, statusViolations, nil
}

func (s *ReconcileStore) readIDMaps(ctx context.Context, tx *sql.Tx, workspaceID string, v2Rows []V2MappedRow) ([]ReconcileIDMap, error) {
	present := make(map[ReplayEntityKey]struct{}, len(v2Rows))
	for _, row := range v2Rows {
		present[row.Target] = struct{}{}
	}
	query := s.workspaceQuery(`SELECT entity_kind,legacy_id,target_kind,v2_id,deleted FROM task_domain_legacy_id_map WHERE workspace_id=?`)
	rows, err := tx.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("read reconcile ID maps: %w", err)
	}
	defer rows.Close()
	result := make([]ReconcileIDMap, 0)
	for rows.Next() {
		var sourceKind, sourceID, targetKind, targetID string
		var deleted bool
		if err := rows.Scan(&sourceKind, &sourceID, &targetKind, &targetID, &deleted); err != nil {
			return nil, err
		}
		target := reconcileStoreKey(reconcileTargetKind(targetKind), targetID)
		if deleted {
			if _, stillPresent := present[target]; !stillPresent {
				continue
			}
		}
		result = append(result, ReconcileIDMap{
			Source: reconcileStoreKey(ReplayEntityKind(sourceKind), sourceID), Target: target,
		})
	}
	return result, rows.Err()
}

func (s *ReconcileStore) deleteMappedTarget(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	mutation ReconcileMutation,
	writtenAt time.Time,
) error {
	if mutation.Target.Kind == ReplayEntityProject {
		var systemRole sql.NullString
		query := s.bind(`SELECT system_role FROM domain_projects_v2 WHERE workspace_id=? AND id=?`, 2)
		if err := tx.QueryRowContext(ctx, query, workspaceID, mutation.Target.SourceID).Scan(&systemRole); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if systemRole.Valid {
			return &ReconcileApplyBlock{Code: ReconcileApplyGeneratedTarget, Reference: replayKeyReference(mutation.Target)}
		}
	}

	var logicalVersion int64
	var deleted bool
	ledgerQuery := s.bind(`SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`, 3)
	err := tx.QueryRowContext(ctx, ledgerQuery, workspaceID, mutation.Source.Kind, mutation.Source.SourceID).Scan(&logicalVersion, &deleted)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !deleted) {
		return &ReconcileApplyBlock{Code: ReconcileApplySourceNotDeleted, Reference: replayKeyReference(mutation.Source)}
	}
	if err != nil {
		return fmt.Errorf("read deletion source ledger: %w", err)
	}

	table, identity, err := reconcileDeleteTable(mutation.Target)
	if err != nil {
		return err
	}
	deleteQuery := s.bind(`DELETE FROM `+table+` WHERE workspace_id=? AND `+identity+`=?`, 2)
	result, err := tx.ExecContext(ctx, deleteQuery, workspaceID, mutation.Target.SourceID)
	if err != nil {
		return fmt.Errorf("delete reconcile target %s: %w", replayKeyReference(mutation.Target), err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return &ReconcileApplyBlock{Code: ReconcileApplyMissingTarget, Reference: replayKeyReference(mutation.Target)}
	}

	updateMap := s.bind(`UPDATE task_domain_legacy_id_map SET deleted=?,source_logical_version=?,updated_at=?
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=? AND target_kind=? AND v2_id=?`, 8)
	deletedValue := any(1)
	if s.dialect == DialectPostgres {
		deletedValue = true
	}
	result, err = tx.ExecContext(ctx, updateMap, deletedValue, logicalVersion, writtenAt.UTC(), workspaceID,
		mutation.Source.Kind, mutation.Source.SourceID, mutation.Target.Kind, mutation.Target.SourceID)
	if err != nil {
		return fmt.Errorf("mark reconcile ID map deleted: %w", err)
	}
	count, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return &ReconcileApplyBlock{Code: ReconcileApplyStaleObservation, Reference: replayKeyReference(mutation.Target), Detail: "ID map changed"}
	}
	return nil
}

func (s *ReconcileStore) workspaceQuery(query string) string { return s.bind(query, 1) }

func (s *ReconcileStore) bind(query string, count int) string {
	if s.dialect != DialectPostgres {
		return query
	}
	for i := 1; i <= count; i++ {
		query = strings.Replace(query, "?", "$"+strconv.Itoa(i), 1)
	}
	return query
}

func reconcileDeleteTable(target ReplayEntityKey) (string, string, error) {
	switch target.Kind {
	case ReplayEntityProject:
		return "domain_projects_v2", "id", nil
	case ReplayEntityRoadmap:
		return "domain_learning_roadmaps_v2", "id", nil
	case ReplayEntityRoadmapNode:
		return "domain_roadmap_nodes_v2", "id", nil
	case ReplayEntityRoadmapEdge:
		return "domain_roadmap_edges_v2", "id", nil
	case ReplayEntityTask:
		return "domain_tasks_v2", "id", nil
	case ReplayEntitySchedule:
		return "domain_task_schedules_v2", "task_id", nil
	case ReplayEntityOccurrence:
		return "domain_task_occurrences_v2", "id", nil
	default:
		return "", "", &ReconcileApplyBlock{Code: ReconcileApplyUnsafeMismatch, Reference: replayKeyReference(target), Detail: "unsupported target kind"}
	}
}

func reconcileTargetKind(kind string) ReplayEntityKind {
	if kind == "schedule" {
		return ReplayEntitySchedule
	}
	return ReplayEntityKind(kind)
}

func reconcileStoreKey(kind ReplayEntityKind, id string) ReplayEntityKey {
	return ReplayEntityKey{Kind: kind, SourceID: id}
}

func putProjectionDigest(target map[ReplayEntityKey]string, key ReplayEntityKey, digest string) error {
	if !validReconcileEntityKind(key.Kind) || strings.TrimSpace(key.SourceID) == "" {
		return &ReconcileBlock{Code: ReconcileBlockInvalidIdentity, Reference: replayKeyReference(key)}
	}
	if _, exists := target[key]; exists {
		return &ReconcileBlock{Code: ReconcileBlockDuplicateIdentity, Reference: replayKeyReference(key), Detail: "frozen projection target"}
	}
	target[key] = digest
	return nil
}

func uniqueReplayKeys(keys []ReplayEntityKey) []ReplayEntityKey {
	seen := make(map[ReplayEntityKey]struct{}, len(keys))
	result := make([]ReplayEntityKey, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return lessReplayKey(result[i], result[j]) })
	return result
}

func digestCanonical(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", hash[:]), nil
}

func canonicalJSONString(value string) (string, error) {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func canonicalExpectedTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func canonicalString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case time.Time:
		return typed.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(value)
	}
}

func canonicalInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		parsed, _ := strconv.ParseInt(canonicalString(value), 10, 64)
		return parsed
	}
}

func canonicalInstant(value any) string {
	if value == nil {
		return ""
	}
	if typed, ok := value.(time.Time); ok {
		return typed.UTC().Format(time.RFC3339Nano)
	}
	raw := canonicalString(value)
	if raw == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func canonicalTimeOfDay(value any) string {
	if typed, ok := value.(time.Time); ok {
		return typed.Format("15:04:05")
	}
	return canonicalString(value)
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validObservedSchedule(values [14]any) bool {
	current := canonicalInt64(values[1])
	generation := canonicalString(values[2])
	revision := canonicalInt64(values[3])
	recurrence := canonicalString(values[6])
	timing := canonicalString(values[7])
	starts := canonicalString(values[9])
	ends := canonicalString(values[10])
	local := canonicalTimeOfDay(values[12])
	duration := canonicalInt64(values[13])
	if current <= 0 || revision != current || !oneOf(generation, "idle", "running", "retry_pending", "failed") || !oneOf(recurrence, "none", "daily", "weekly", "monthly") || !oneOf(timing, "unscheduled", "date", "time_block") {
		return false
	}
	if ends != "" && (starts == "" || ends < starts) {
		return false
	}
	switch timing {
	case "unscheduled":
		return recurrence == "none" && starts == "" && local == "" && duration == 0
	case "date":
		return starts != "" && local == "" && duration == 0
	case "time_block":
		return starts != "" && local != "" && duration > 0
	default:
		return false
	}
}

func validObservedOccurrence(values [17]any) bool {
	status := canonicalString(values[7])
	completed := canonicalInstant(values[8])
	reason := strings.TrimSpace(canonicalString(values[14]))
	next := strings.TrimSpace(canonicalString(values[15]))
	if !oneOf(status, "open", "active", "blocked", "done", "skipped", "cancelled") {
		return false
	}
	if (status == "done") != (completed != "") {
		return false
	}
	if status == "blocked" {
		return reason != "" && next != ""
	}
	return reason == "" && next == ""
}

type projectDigestRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Horizon    string `json:"horizon"`
	Status     string `json:"status"`
	SystemRole string `json:"system_role"`
}

type roadmapDigestRow struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type roadmapNodeDigestRow struct {
	ID             string  `json:"id"`
	ProjectID      string  `json:"project_id"`
	RoadmapID      string  `json:"roadmap_id"`
	ParentID       string  `json:"parent_id"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	NodeType       string  `json:"node_type"`
	Status         string  `json:"status"`
	Position       float64 `json:"position"`
	LegacyMetadata string  `json:"legacy_metadata"`
}

type roadmapEdgeDigestRow struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	RoadmapID  string `json:"roadmap_id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	EdgeType   string `json:"edge_type"`
}

type taskDigestRow struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	RoadmapNodeID   string  `json:"roadmap_node_id"`
	NoteID          string  `json:"note_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	LifecycleStatus string  `json:"lifecycle_status"`
	Priority        int     `json:"priority"`
	SortOrder       float64 `json:"sort_order"`
}

type scheduleDigestRow struct {
	TaskID           string `json:"task_id"`
	CurrentRevision  int64  `json:"current_revision"`
	GenerationStatus string `json:"generation_status"`
	ScheduleRevision int64  `json:"schedule_revision"`
	EffectiveFrom    string `json:"effective_from"`
	EffectiveTo      string `json:"effective_to"`
	RecurrenceType   string `json:"recurrence_type"`
	TimingType       string `json:"timing_type"`
	Timezone         string `json:"timezone"`
	StartsOn         string `json:"starts_on"`
	EndsOn           string `json:"ends_on"`
	RecurrenceRule   string `json:"recurrence_rule"`
	LocalStartTime   string `json:"local_start_time"`
	DurationMinutes  int    `json:"duration_minutes"`
}

type occurrenceDigestRow struct {
	ID                        string `json:"id"`
	TaskID                    string `json:"task_id"`
	OccurrenceKey             string `json:"occurrence_key"`
	PlannedDate               string `json:"planned_date"`
	PlannedStartAt            string `json:"planned_start_at"`
	PlannedEndAt              string `json:"planned_end_at"`
	DueAt                     string `json:"due_at"`
	ExecutionStatus           string `json:"execution_status"`
	CompletedAt               string `json:"completed_at"`
	Location                  string `json:"location"`
	CalendarKind              string `json:"calendar_kind"`
	CalendarNotes             string `json:"calendar_notes"`
	NoteID                    string `json:"note_id"`
	AllDayEndDate             string `json:"all_day_end_date"`
	BlockedReason             string `json:"blocked_reason"`
	NextAction                string `json:"next_action"`
	GeneratedScheduleRevision int64  `json:"generated_schedule_revision"`
}
