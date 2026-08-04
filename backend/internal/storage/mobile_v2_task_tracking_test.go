package storage

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestTrackedTaskDomainWriterUsesContractEntityTypeForDeletedOccurrences(t *testing.T) {
	changes := NewMobileV2TaskChanges()
	writer := TrackTaskDomainWriter(trackingTaskDomainWriterFake{}, changes)
	deleter, ok := writer.(taskdomain.TaskAggregateDeleter)
	if !ok {
		t.Fatal("tracked writer does not preserve task aggregate delete capability")
	}

	err := deleter.DeleteTaskAggregate(context.Background(), taskdomain.TaskAggregateDelete{
		TaskID: "task-1",
		ExpectedRevisions: taskdomain.AggregateExpectedRevisions{
			Task:        3,
			Occurrences: map[string]int64{"occurrence-1": 5},
		},
	})
	if err != nil {
		t.Fatalf("delete task aggregate: %v", err)
	}

	snapshot := changes.Snapshot()
	if len(snapshot.Deleted) != 2 {
		t.Fatalf("deleted entities = %#v", snapshot.Deleted)
	}
	foundOccurrence := false
	for _, deleted := range snapshot.Deleted {
		if deleted.EntityID == "occurrence-1" {
			foundOccurrence = true
			if deleted.EntityType != "task_occurrence" || deleted.Revision != 6 {
				t.Fatalf("occurrence tombstone = %#v", deleted)
			}
		}
	}
	if !foundOccurrence {
		t.Fatalf("occurrence tombstone missing: %#v", snapshot.Deleted)
	}
}

type trackingTaskDomainWriterFake struct{}

func (trackingTaskDomainWriterFake) EnsureSystemProjects(context.Context) error { return nil }
func (trackingTaskDomainWriterFake) SaveProject(context.Context, taskdomain.ProjectWrite) error {
	return nil
}
func (trackingTaskDomainWriterFake) DeleteProject(context.Context, string, int64) error { return nil }
func (trackingTaskDomainWriterFake) CreateTaskAggregate(context.Context, taskdomain.TaskAggregateSnapshot) error {
	return nil
}
func (trackingTaskDomainWriterFake) SaveTaskAggregate(context.Context, taskdomain.TaskAggregateWrite) error {
	return nil
}
func (trackingTaskDomainWriterFake) InstallScheduleVersion(context.Context, taskdomain.ScheduleVersionInstall) error {
	return nil
}
func (trackingTaskDomainWriterFake) DeleteTaskAggregate(context.Context, taskdomain.TaskAggregateDelete) error {
	return nil
}
