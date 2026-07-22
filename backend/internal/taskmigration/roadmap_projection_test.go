package taskmigration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestMapLegacyTaskDomainPreservesLearningRoadmapGraphTaskLinkAndOccurrenceNote(t *testing.T) {
	t.Parallel()

	rows := validLegacyRows()
	rows.Projects = append(rows.Projects, LegacyProjectRow{ID: "learning-1", Name: "Go learning", Type: LegacyProjectLearning})
	rows.Roadmaps = []LegacyRoadmapRow{{
		ID: "roadmap-1", ProjectID: "learning-1", Title: "Learn Go", Goal: "Ship a service", Status: "active",
	}}
	// Deliberately child-first: the mapper must emit a stable parent-before-child graph.
	rows.RoadmapNodes = []LegacyRoadmapNodeRow{
		{
			ID: "node-child", RoadmapID: "roadmap-1", ParentID: "node-root", Type: "task", Title: "Write API",
			Description: "Implement one endpoint", PathType: "recommended", Status: "active",
			Deliverable: "working endpoint", AcceptanceCriteria: "integration test passes",
			CanvasX: 320.5, CanvasY: 180.25, OrderIndex: 20, ArticleSearchQueries: []string{"Go HTTP", "httptest"},
		},
		{
			ID: "node-root", RoadmapID: "roadmap-1", Type: "phase", Title: "Foundation",
			Description: "Language basics", PathType: "required", Status: "done",
			Deliverable: "notes", AcceptanceCriteria: "quiz complete",
			CanvasX: 10, CanvasY: 20, OrderIndex: 10, ArticleSearchQueries: []string{"Go tour"},
		},
	}
	rows.RoadmapEdges = []LegacyRoadmapEdgeRow{{
		ID: "edge-1", RoadmapID: "roadmap-1", SourceNodeID: "node-root", TargetNodeID: "node-child", Style: "solid",
	}}
	rows.Tasks = []LegacyTaskRow{{
		ID: "task-1", ProjectID: "learning-1", RoadmapNodeID: "node-child", ExecutionType: LegacyExecutionRecurring,
		Title: "Daily API practice", Priority: 1,
	}}
	rows.Rules = []LegacyRuleRow{{
		ID: "rule-1", TaskID: "task-1", RecurrenceType: taskdomain.RecurrenceDaily,
		TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", Interval: 1,
	}}
	rows.Occurrences = []LegacyOccurrenceRow{{
		TaskID: "task-1", OccurrenceDate: "2026-07-22", Status: taskdomain.ExecutionStatusActive,
		Note: "Reviewed error handling",
	}}
	preflight := validMapperPreflight()
	preflight.Projects = append(preflight.Projects, ProjectDecision{
		LegacyID: "learning-1", TargetID: "learning-1", Name: "Go learning", Kind: "learning", Horizon: "long",
	})
	preflight.Tasks = []TaskDecision{{LegacyID: "task-1", TargetProjectID: "learning-1"}}

	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		t.Fatalf("MapLegacyTaskDomain: %v", err)
	}
	if len(projection.Roadmaps) != 1 || projection.Roadmaps[0].ProjectID != "learning-1" || projection.Roadmaps[0].Description != "Ship a service" {
		t.Fatalf("roadmap projection = %#v", projection.Roadmaps)
	}
	if got := []string{projection.RoadmapNodes[0].ID, projection.RoadmapNodes[1].ID}; !reflect.DeepEqual(got, []string{"node-root", "node-child"}) {
		t.Fatalf("node dependency order = %#v", got)
	}
	child := projection.RoadmapNodes[1]
	if child.ProjectID != "learning-1" || child.RoadmapID != "roadmap-1" || child.ParentID != "node-root" || child.Position != 20 || child.LegacyNodeType != "task" || child.PathType != "recommended" || child.Deliverable != "working endpoint" || child.AcceptanceCriteria != "integration test passes" || child.CanvasX != 320.5 || child.CanvasY != 180.25 || !reflect.DeepEqual(child.ArticleSearchQueries, []string{"Go HTTP", "httptest"}) {
		t.Fatalf("child node metadata = %#v", child)
	}
	if len(projection.RoadmapEdges) != 1 || projection.RoadmapEdges[0].FromNodeID != "node-root" || projection.RoadmapEdges[0].ToNodeID != "node-child" || projection.RoadmapEdges[0].EdgeType != "suggested_order" {
		t.Fatalf("edge projection = %#v", projection.RoadmapEdges)
	}
	if task := projectedTask(t, projection, "task-1"); task.RoadmapNodeID != "node-child" {
		t.Fatalf("task roadmap link = %#v", task)
	}
	occurrence := projectedOccurrenceByKey(t, projection, "task-1", "2026-07-22")
	if occurrence.CalendarNotes != "Reviewed error handling" || occurrence.BlockedReason != "" || occurrence.NextAction != "" {
		t.Fatalf("occurrence note was mixed with execution metadata: %#v", occurrence)
	}
	for _, source := range []struct {
		kind LegacyEntityKind
		id   string
	}{
		{LegacyEntityRoadmap, "roadmap-1"},
		{LegacyEntityRoadmapNode, "node-root"},
		{LegacyEntityRoadmapNode, "node-child"},
		{LegacyEntityRoadmapEdge, "edge-1"},
	} {
		_ = idMapEntry(t, projection, source.kind, source.id)
	}
}

func TestMapLegacyTaskDomainRejectsCrossProjectRoadmapTaskLink(t *testing.T) {
	t.Parallel()

	rows := validLegacyRows()
	rows.Projects = append(rows.Projects,
		LegacyProjectRow{ID: "learning-a", Name: "A", Type: LegacyProjectLearning},
		LegacyProjectRow{ID: "learning-b", Name: "B", Type: LegacyProjectLearning},
	)
	rows.Roadmaps = []LegacyRoadmapRow{{ID: "roadmap-a", ProjectID: "learning-a", Title: "A", Status: "active"}}
	rows.RoadmapNodes = []LegacyRoadmapNodeRow{{ID: "node-a", RoadmapID: "roadmap-a", Type: "task", Title: "A task", Status: "todo"}}
	rows.Tasks = []LegacyTaskRow{{ID: "task-b", ProjectID: "learning-b", RoadmapNodeID: "node-a", ExecutionType: LegacyExecutionSingle, Title: "B task"}}
	preflight := validMapperPreflight()
	preflight.Projects = append(preflight.Projects,
		ProjectDecision{LegacyID: "learning-a", TargetID: "learning-a", Name: "A", Kind: "learning", Horizon: "long"},
		ProjectDecision{LegacyID: "learning-b", TargetID: "learning-b", Name: "B", Kind: "learning", Horizon: "long"},
	)
	preflight.Tasks = []TaskDecision{{LegacyID: "task-b", TargetProjectID: "learning-b"}}

	projection, err := MapLegacyTaskDomain(preflight, rows)
	var block *MapperBlock
	if !errors.As(err, &block) || block.Code != MapperBlockInvalidRoadmapStructure || len(projection.Projects) != 0 {
		t.Fatalf("MapLegacyTaskDomain projection=%#v error=%v, want fail-closed cross-project link", projection, err)
	}
}

func TestV2ProjectionWriterPersistsRoadmapBeforeLinkedTaskAndRoundTripsMetadataSQLite(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha", "beta")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	input := roadmapProjectionWriterInput("alpha")
	writeProjectionTransaction(t, db, writer, input)

	assertProjectionWriterCount(t, db, "domain_learning_roadmaps_v2", "alpha", 1)
	assertProjectionWriterCount(t, db, "domain_roadmap_nodes_v2", "alpha", 2)
	assertProjectionWriterCount(t, db, "domain_roadmap_edges_v2", "alpha", 1)

	var roadmapNodeID, calendarNotes string
	if err := db.QueryRow(`SELECT roadmap_node_id FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&roadmapNodeID); err != nil {
		t.Fatal(err)
	}
	if roadmapNodeID != "node-child" {
		t.Fatalf("roadmap_node_id=%q", roadmapNodeID)
	}
	if err := db.QueryRow(`SELECT calendar_notes FROM domain_task_occurrences_v2 WHERE workspace_id='alpha' AND id='occurrence-1'`).Scan(&calendarNotes); err != nil {
		t.Fatal(err)
	}
	if calendarNotes != "legacy occurrence note" {
		t.Fatalf("calendar_notes=%q", calendarNotes)
	}
	var metadataJSON string
	if err := db.QueryRow(`SELECT legacy_metadata FROM domain_roadmap_nodes_v2 WHERE workspace_id='alpha' AND id='node-child'`).Scan(&metadataJSON); err != nil {
		t.Fatal(err)
	}
	var metadata roadmapNodeLegacyMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode node metadata %q: %v", metadataJSON, err)
	}
	if metadata.LegacyNodeType != "task" || metadata.PathType != "recommended" || metadata.Deliverable != "service" || metadata.AcceptanceCriteria != "tests pass" || metadata.CanvasX != 320.5 || metadata.CanvasY != 180.25 || !reflect.DeepEqual(metadata.ArticleSearchQueries, []string{"Go HTTP"}) {
		t.Fatalf("round-tripped metadata = %#v", metadata)
	}

	// The same logical IDs may exist in another workspace, but a task in alpha
	// may never resolve a node owned only by beta.
	if _, err := db.Exec(`INSERT INTO domain_projects_v2(workspace_id,id,name,kind,horizon,status,revision,created_at,updated_at)
		VALUES('beta','project-beta','Beta','learning','long','active',1,'2026-07-22','2026-07-22');
		INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,revision,created_at,updated_at)
		VALUES('beta','roadmap-beta','project-beta','active','Beta',1,'2026-07-22','2026-07-22');
		INSERT INTO domain_roadmap_nodes_v2(workspace_id,id,project_id,roadmap_id,title,node_type,status,revision,created_at,updated_at)
		VALUES('beta','beta-only-node','project-beta','roadmap-beta','Beta node','topic','available',1,'2026-07-22','2026-07-22')`); err != nil {
		t.Fatalf("seed beta roadmap: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO domain_tasks_v2(workspace_id,id,project_id,roadmap_node_id,title,lifecycle_status,revision,created_at,updated_at)
		VALUES('alpha','cross-workspace','project-1','beta-only-node','bad','active',1,'2026-07-22','2026-07-22')`); err == nil {
		t.Fatal("cross-workspace roadmap link was accepted")
	}
}

func TestV2ProjectionWriterWriteSnapshotFailsClosedWithoutRoadmapOutboxLedger(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, err := NewV2ProjectionWriter(DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	input := roadmapProjectionWriterInput("alpha")
	// Seed only kinds covered by the current legacy trigger/outbox manifest.
	covered := input
	covered.SourceVersions = []ProjectionSourceVersion{
		{EntityKind: LegacyEntityProject, LegacyID: "legacy-project-1", LogicalVersion: 1},
		{EntityKind: LegacyEntityTask, LegacyID: "legacy-task-1", LogicalVersion: 1},
	}
	seedProjectionSourceVersions(t, db, covered)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = writer.WriteSnapshot(context.Background(), tx, "alpha", input.Projection, input.WrittenAt)
	_ = tx.Rollback()
	var conflict *ProjectionWriteConflictError
	if !errors.As(err, &conflict) || conflict.Code != ProjectionWriteConflictMissingSourceVersion {
		t.Fatalf("WriteSnapshot error=%v, want missing roadmap source version", err)
	}
	assertProjectionWriterCount(t, db, "domain_projects_v2", "alpha", 0)
}

func TestReplayProjectionApplierResolvesTaskRoadmapNodeThroughFrozenIDMap(t *testing.T) {
	db := openProjectionWriterSQLite(t, "alpha")
	writer, _ := NewV2ProjectionWriter(DialectSQLite)
	input := roadmapProjectionWriterInput("alpha")
	writeProjectionTransaction(t, db, writer, input)
	setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 2, false)

	event := replayTaskEvent(1201, 2, "Move roadmap task", "active", "2026-07-22")
	event.AfterImage["roadmap_node_id"] = "legacy-node-root"
	applyReplayProjection(t, db, "alpha", event)
	var nodeID string
	if err := db.QueryRow(`SELECT roadmap_node_id FROM domain_tasks_v2 WHERE workspace_id='alpha' AND id='task-1'`).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if nodeID != "node-root" {
		t.Fatalf("roadmap_node_id=%q, want mapped node-root", nodeID)
	}

	setReplaySourceVersion(t, db, "alpha", ReplayEntityTask, "legacy-task-1", 3, false)
	bad := replayTaskEvent(1202, 3, "Bad roadmap task", "active", "2026-07-22")
	bad.AfterImage["roadmap_node_id"] = "missing-legacy-node"
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	applier, _ := NewReplayProjectionApplier("alpha", DialectSQLite)
	err = applier.Apply(context.Background(), tx, []ReplayEvent{bad})
	_ = tx.Rollback()
	var conflict *ReplayProjectionConflictError
	if !errors.As(err, &conflict) || conflict.Code != ReplayProjectionConflictDependency {
		t.Fatalf("missing roadmap node error=%v, want dependency conflict", err)
	}
}

func TestReplayPhysicalTimestampAliasesRemainReadable(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		image   ReplayImage
		aliases []string
		want    string
	}{
		{name: "sqlite task due", image: ReplayImage{"due": "2026-07-25T01:02:03Z"}, aliases: []string{"due_at", "due"}, want: "2026-07-25T01:02:03Z"},
		{name: "postgres event start", image: ReplayImage{"start_at": "2026-07-25T02:03:04Z"}, aliases: []string{"start_time", "start_at"}, want: "2026-07-25T02:03:04Z"},
		{name: "postgres event end", image: ReplayImage{"end_at": "2026-07-25T03:04:05Z"}, aliases: []string{"end_time", "end_at"}, want: "2026-07-25T03:04:05Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := replayTimeFieldAliases(test.image, true, test.aliases...)
			if err != nil || got == nil || got.UTC().Format(time.RFC3339) != test.want {
				t.Fatalf("replayTimeFieldAliases=%v/%v, want %s", got, err, test.want)
			}
		})
	}
}

func roadmapProjectionWriterInput(workspaceID string) V2ProjectionWrite {
	sources := []ProjectionSourceVersion{
		{EntityKind: LegacyEntityProject, LegacyID: "legacy-project-1", LogicalVersion: 1},
		{EntityKind: LegacyEntityRoadmap, LegacyID: "legacy-roadmap-1", LogicalVersion: 1},
		{EntityKind: LegacyEntityRoadmapNode, LegacyID: "legacy-node-root", LogicalVersion: 1},
		{EntityKind: LegacyEntityRoadmapNode, LegacyID: "legacy-node-child", LogicalVersion: 1},
		{EntityKind: LegacyEntityRoadmapEdge, LegacyID: "legacy-edge-1", LogicalVersion: 1},
		{EntityKind: LegacyEntityTask, LegacyID: "legacy-task-1", LogicalVersion: 1},
	}
	return V2ProjectionWrite{
		WorkspaceID:    workspaceID,
		WrittenAt:      time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		SourceVersions: sources,
		Projection: V2Projection{
			Projects: []V2ProjectProjection{{ID: "project-1", Name: "Learning", Kind: "learning", Horizon: "long"}},
			Roadmaps: []V2LearningRoadmapProjection{{ID: "roadmap-1", ProjectID: "project-1", Status: "active", Title: "Learn Go", Description: "Ship"}},
			// Deliberately child-first to prove the writer establishes dependency order.
			RoadmapNodes: []V2RoadmapNodeProjection{
				{ID: "node-child", ProjectID: "project-1", RoadmapID: "roadmap-1", ParentID: "node-root", Title: "API", NodeType: "topic", Status: "in_progress", Position: 20, LegacyNodeType: "task", PathType: "recommended", Deliverable: "service", AcceptanceCriteria: "tests pass", CanvasX: 320.5, CanvasY: 180.25, ArticleSearchQueries: []string{"Go HTTP"}},
				{ID: "node-root", ProjectID: "project-1", RoadmapID: "roadmap-1", Title: "Basics", NodeType: "stage", Status: "mastered", Position: 10, LegacyNodeType: "phase", PathType: "required"},
			},
			RoadmapEdges: []V2RoadmapEdgeProjection{{ID: "edge-1", ProjectID: "project-1", RoadmapID: "roadmap-1", FromNodeID: "node-root", ToNodeID: "node-child", EdgeType: "suggested_order"}},
			Tasks:        []V2TaskProjection{{ID: "task-1", ProjectID: "project-1", RoadmapNodeID: "node-child", Title: "Practice", LifecycleStatus: taskdomain.TaskLifecycleActive}},
			Schedules:    []V2ScheduleProjection{{TaskID: "task-1", RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-07-22", Interval: 1}},
			Occurrences:  []V2OccurrenceProjection{{ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "once", ExecutionStatus: taskdomain.ExecutionStatusActive, PlannedDate: "2026-07-22", CalendarNotes: "legacy occurrence note", GeneratedScheduleRevision: 1}},
			IDMap: []V2IDMapEntry{
				{LegacyKind: LegacyEntityProject, LegacyID: "legacy-project-1", TargetProjectID: "project-1"},
				{LegacyKind: LegacyEntityRoadmap, LegacyID: "legacy-roadmap-1", TargetProjectID: "project-1", TargetRoadmapID: "roadmap-1"},
				{LegacyKind: LegacyEntityRoadmapNode, LegacyID: "legacy-node-root", TargetProjectID: "project-1", TargetRoadmapNodeID: "node-root"},
				{LegacyKind: LegacyEntityRoadmapNode, LegacyID: "legacy-node-child", TargetProjectID: "project-1", TargetRoadmapNodeID: "node-child"},
				{LegacyKind: LegacyEntityRoadmapEdge, LegacyID: "legacy-edge-1", TargetProjectID: "project-1", TargetRoadmapEdgeID: "edge-1"},
				{LegacyKind: LegacyEntityTask, LegacyID: "legacy-task-1", TargetProjectID: "project-1", TargetTaskID: "task-1", TargetScheduleID: "task-1", TargetOccurrenceID: "occurrence-1"},
			},
		},
	}
}
