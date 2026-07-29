package storage

import (
	"context"
	"sort"
	"sync"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// MobileV2TaskChanges records the smallest useful task-domain projection set
// while a fenced transaction is executing. Providers publish this set only
// when the transaction did not already write a mobile-v2 command receipt.
type MobileV2TaskChanges struct {
	mu            sync.Mutex
	projectIDs    map[string]struct{}
	taskIDs       map[string]struct{}
	occurrenceIDs map[string]struct{}
	deleted       map[string]MobileV2DeletedEntity
	fullTaskCore  bool
}

type MobileV2DeletedEntity struct {
	EntityType string
	EntityID   string
	Revision   int64
}

type MobileV2TaskChangeSnapshot struct {
	ProjectIDs    map[string]struct{}
	TaskIDs       map[string]struct{}
	OccurrenceIDs map[string]struct{}
	Deleted       []MobileV2DeletedEntity
	FullTaskCore  bool
}

func NewMobileV2TaskChanges() *MobileV2TaskChanges {
	return &MobileV2TaskChanges{
		projectIDs: make(map[string]struct{}), taskIDs: make(map[string]struct{}),
		occurrenceIDs: make(map[string]struct{}), deleted: make(map[string]MobileV2DeletedEntity),
	}
}

func (changes *MobileV2TaskChanges) Snapshot() MobileV2TaskChangeSnapshot {
	if changes == nil {
		return MobileV2TaskChangeSnapshot{}
	}
	changes.mu.Lock()
	defer changes.mu.Unlock()
	return MobileV2TaskChangeSnapshot{
		ProjectIDs: cloneIDSet(changes.projectIDs), TaskIDs: cloneIDSet(changes.taskIDs),
		OccurrenceIDs: cloneIDSet(changes.occurrenceIDs), Deleted: cloneDeletedEntities(changes.deleted),
		FullTaskCore: changes.fullTaskCore,
	}
}

func (snapshot MobileV2TaskChangeSnapshot) Empty() bool {
	return !snapshot.FullTaskCore && len(snapshot.ProjectIDs) == 0 &&
		len(snapshot.TaskIDs) == 0 && len(snapshot.OccurrenceIDs) == 0 && len(snapshot.Deleted) == 0
}

func TrackTaskDomainWriter(delegate taskdomain.TaskDomainWriter, changes *MobileV2TaskChanges) taskdomain.TaskDomainWriter {
	if delegate == nil || changes == nil {
		return delegate
	}
	return trackedTaskDomainWriter{delegate: delegate, changes: changes}
}

func TrackProjectWriter(delegate taskdomain.ProjectWriter, changes *MobileV2TaskChanges) taskdomain.ProjectWriter {
	if delegate == nil || changes == nil {
		return delegate
	}
	return trackedProjectWriter{delegate: delegate, changes: changes}
}

func TrackRoadmapWriter(delegate taskdomain.RoadmapWriter, changes *MobileV2TaskChanges) taskdomain.RoadmapWriter {
	if delegate == nil || changes == nil {
		return delegate
	}
	return trackedRoadmapWriter{delegate: delegate, changes: changes}
}

func TrackScheduleWriter(delegate taskdomain.ScheduleCommandWriter, changes *MobileV2TaskChanges) taskdomain.ScheduleCommandWriter {
	if delegate == nil || changes == nil {
		return delegate
	}
	return trackedScheduleWriter{delegate: delegate, changes: changes}
}

func (changes *MobileV2TaskChanges) TrackGenerationInsert(insert taskdomain.GenerationInsert) {
	if changes == nil {
		return
	}
	changes.markTask(insert.TaskID)
	for _, occurrence := range insert.Occurrences {
		changes.markOccurrence(occurrence.ID)
	}
}

func (changes *MobileV2TaskChanges) TrackGenerationCompletion(completion taskdomain.GenerationCompletion) {
	if changes == nil {
		return
	}
	changes.markTask(completion.TaskID)
}

type trackedProjectWriter struct {
	delegate taskdomain.ProjectWriter
	changes  *MobileV2TaskChanges
}

func (writer trackedProjectWriter) EnsureSystemProjects(ctx context.Context) error {
	if err := writer.delegate.EnsureSystemProjects(ctx); err != nil {
		return err
	}
	writer.changes.markFullTaskCore()
	return nil
}

func (writer trackedProjectWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
	if err := writer.delegate.SaveProject(ctx, write); err != nil {
		return err
	}
	writer.changes.markProject(write.Project.ID)
	return nil
}

func (writer trackedProjectWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	if err := writer.delegate.DeleteProject(ctx, projectID, expectedRevision); err != nil {
		return err
	}
	writer.changes.markDeleted("project", projectID, expectedRevision+1)
	return nil
}

type trackedTaskDomainWriter struct {
	delegate taskdomain.TaskDomainWriter
	changes  *MobileV2TaskChanges
}

func (writer trackedTaskDomainWriter) EnsureSystemProjects(ctx context.Context) error {
	return trackedProjectWriter{delegate: writer.delegate, changes: writer.changes}.EnsureSystemProjects(ctx)
}

func (writer trackedTaskDomainWriter) SaveProject(ctx context.Context, write taskdomain.ProjectWrite) error {
	return trackedProjectWriter{delegate: writer.delegate, changes: writer.changes}.SaveProject(ctx, write)
}

func (writer trackedTaskDomainWriter) DeleteProject(ctx context.Context, projectID string, expectedRevision int64) error {
	return trackedProjectWriter{delegate: writer.delegate, changes: writer.changes}.DeleteProject(ctx, projectID, expectedRevision)
}

func (writer trackedTaskDomainWriter) CreateTaskAggregate(ctx context.Context, snapshot taskdomain.TaskAggregateSnapshot) error {
	if err := writer.delegate.CreateTaskAggregate(ctx, snapshot); err != nil {
		return err
	}
	writer.changes.markProject(snapshot.Task.ProjectID)
	writer.changes.markTask(snapshot.Task.ID)
	for _, occurrence := range snapshot.Occurrences {
		writer.changes.markOccurrence(occurrence.ID)
	}
	return nil
}

func (writer trackedTaskDomainWriter) SaveTaskAggregate(ctx context.Context, write taskdomain.TaskAggregateWrite) error {
	if err := writer.delegate.SaveTaskAggregate(ctx, write); err != nil {
		return err
	}
	writer.changes.markTask(write.Aggregate.TaskID)
	if write.Task != nil {
		writer.changes.markProject(write.Task.ProjectID)
	}
	for _, occurrence := range write.Aggregate.Occurrences {
		writer.changes.markOccurrence(occurrence.ID)
	}
	return nil
}

func (writer trackedTaskDomainWriter) InstallScheduleVersion(ctx context.Context, install taskdomain.ScheduleVersionInstall) error {
	if err := writer.delegate.InstallScheduleVersion(ctx, install); err != nil {
		return err
	}
	writer.changes.markTask(install.TaskID)
	return nil
}

type trackedRoadmapWriter struct {
	delegate taskdomain.RoadmapWriter
	changes  *MobileV2TaskChanges
}

func (writer trackedRoadmapWriter) CreateRoadmap(ctx context.Context, roadmap taskdomain.LearningRoadmap) error {
	if err := writer.delegate.CreateRoadmap(ctx, roadmap); err != nil {
		return err
	}
	writer.changes.markProject(roadmap.ProjectID)
	writer.changes.markFullTaskCore()
	return nil
}

func (writer trackedRoadmapWriter) CreateRoadmapNode(ctx context.Context, node taskdomain.RoadmapNode) error {
	if err := writer.delegate.CreateRoadmapNode(ctx, node); err != nil {
		return err
	}
	writer.changes.markFullTaskCore()
	return nil
}

func (writer trackedRoadmapWriter) SaveRoadmapNode(ctx context.Context, write taskdomain.RoadmapNodeWrite) error {
	if err := writer.delegate.SaveRoadmapNode(ctx, write); err != nil {
		return err
	}
	writer.changes.markFullTaskCore()
	return nil
}

func (writer trackedRoadmapWriter) DeleteRoadmapNode(ctx context.Context, nodeID string, expectedRevision int64) error {
	if err := writer.delegate.DeleteRoadmapNode(ctx, nodeID, expectedRevision); err != nil {
		return err
	}
	writer.changes.markFullTaskCore()
	writer.changes.markDeleted("roadmap_node", nodeID, expectedRevision+1)
	return nil
}

type trackedScheduleWriter struct {
	delegate taskdomain.ScheduleCommandWriter
	changes  *MobileV2TaskChanges
}

func (writer trackedScheduleWriter) ApplyOccurrenceReschedule(ctx context.Context, write taskdomain.OccurrenceRescheduleWrite) error {
	if err := writer.delegate.ApplyOccurrenceReschedule(ctx, write); err != nil {
		return err
	}
	writer.changes.markTask(write.TaskID)
	writer.changes.markOccurrence(write.After.Record.ID)
	return nil
}

func (writer trackedScheduleWriter) ApplyScheduleVersionChange(ctx context.Context, write taskdomain.ScheduleVersionChangeWrite) error {
	if err := writer.delegate.ApplyScheduleVersionChange(ctx, write); err != nil {
		return err
	}
	writer.changes.markTask(write.TaskID)
	for _, occurrence := range write.UpsertOccurrences {
		writer.changes.markOccurrence(occurrence.Record.ID)
	}
	for occurrenceID := range write.DeleteOccurrenceRevisions {
		writer.changes.markDeleted(
			"task_occurrence", occurrenceID, write.DeleteOccurrenceRevisions[occurrenceID]+1,
		)
	}
	return nil
}

func (changes *MobileV2TaskChanges) markProject(id string) {
	changes.mark(changes.projectIDs, id)
}

func (changes *MobileV2TaskChanges) markTask(id string) {
	changes.mark(changes.taskIDs, id)
}

func (changes *MobileV2TaskChanges) markOccurrence(id string) {
	changes.mark(changes.occurrenceIDs, id)
}

func (changes *MobileV2TaskChanges) mark(target map[string]struct{}, id string) {
	if changes == nil || id == "" {
		return
	}
	changes.mu.Lock()
	target[id] = struct{}{}
	changes.mu.Unlock()
}

func (changes *MobileV2TaskChanges) markDeleted(entityType, entityID string, revision int64) {
	if changes == nil || entityType == "" || entityID == "" || revision < 1 {
		return
	}
	changes.mu.Lock()
	changes.deleted[entityType+"\x00"+entityID] = MobileV2DeletedEntity{
		EntityType: entityType, EntityID: entityID, Revision: revision,
	}
	changes.mu.Unlock()
}

func cloneDeletedEntities(source map[string]MobileV2DeletedEntity) []MobileV2DeletedEntity {
	result := make([]MobileV2DeletedEntity, 0, len(source))
	for _, entity := range source {
		result = append(result, entity)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].EntityType != result[j].EntityType {
			return result[i].EntityType < result[j].EntityType
		}
		return result[i].EntityID < result[j].EntityID
	})
	return result
}

func (changes *MobileV2TaskChanges) markFullTaskCore() {
	if changes == nil {
		return
	}
	changes.mu.Lock()
	changes.fullTaskCore = true
	changes.mu.Unlock()
}

func cloneIDSet(source map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(source))
	for id := range source {
		result[id] = struct{}{}
	}
	return result
}
