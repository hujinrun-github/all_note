package taskdomain

import (
	"errors"
	"testing"
)

func TestTaskLifecycleLegalTransitions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		from TaskLifecycleStatus
		want TaskLifecycleStatus
		act  func(TaskLifecycleStatus) (TaskLifecycleStatus, error)
	}{
		{name: "publish draft", from: TaskLifecycleDraft, want: TaskLifecycleActive, act: PublishTask},
		{name: "pause active", from: TaskLifecycleActive, want: TaskLifecyclePaused, act: PauseTask},
		{name: "resume paused", from: TaskLifecyclePaused, want: TaskLifecycleActive, act: ResumeTask},
		{name: "complete active", from: TaskLifecycleActive, want: TaskLifecycleCompleted, act: CompleteTask},
		{name: "cancel active", from: TaskLifecycleActive, want: TaskLifecycleCancelled, act: CancelTask},
		{name: "cancel paused", from: TaskLifecyclePaused, want: TaskLifecycleCancelled, act: CancelTask},
		{name: "reopen completed occurrence", from: TaskLifecycleCompleted, want: TaskLifecycleActive, act: ReopenTaskFromOccurrence},
		{name: "restore cancelled", from: TaskLifecycleCancelled, want: TaskLifecycleActive, act: RestoreTask},
		{name: "archive completed", from: TaskLifecycleCompleted, want: TaskLifecycleArchived, act: ArchiveTask},
		{name: "archive cancelled", from: TaskLifecycleCancelled, want: TaskLifecycleArchived, act: ArchiveTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.act(tc.from)
			if err != nil {
				t.Fatalf("transition error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTaskLifecycleIllegalTransitionsReturnStableError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		from TaskLifecycleStatus
		act  func(TaskLifecycleStatus) (TaskLifecycleStatus, error)
	}{
		{name: "draft cannot complete", from: TaskLifecycleDraft, act: CompleteTask},
		{name: "active cannot archive", from: TaskLifecycleActive, act: ArchiveTask},
		{name: "cancelled task cannot reopen via occurrence", from: TaskLifecycleCancelled, act: ReopenTaskFromOccurrence},
		{name: "completed cannot publish", from: TaskLifecycleCompleted, act: PublishTask},
		{name: "archived cannot restore", from: TaskLifecycleArchived, act: RestoreTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.act(tc.from)
			if got != tc.from {
				t.Fatalf("failed transition changed status to %q", got)
			}
			if !errors.Is(err, ErrInvalidTaskTransition) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidTaskTransition)
			}
			if ErrorCodeOf(err) != ErrorCodeInvalidTaskTransition {
				t.Fatalf("error code = %q, want %q", ErrorCodeOf(err), ErrorCodeInvalidTaskTransition)
			}
			if err.Error() != string(ErrorCodeInvalidTaskTransition) {
				t.Fatalf("error %q leaked internal lifecycle values", err)
			}
		})
	}
}

func TestOrdinaryTaskPatchCannotChangeLifecycle(t *testing.T) {
	t.Parallel()

	status := TaskLifecycleCancelled
	task := TaskDefinition{Title: "before", LifecycleStatus: TaskLifecycleActive}
	got, err := PatchTaskDefinition(task, TaskPatch{Title: "after", LifecycleStatus: &status})
	if !errors.Is(err, ErrLifecyclePatchForbidden) {
		t.Fatalf("PatchTaskDefinition() error = %v, want %v", err, ErrLifecyclePatchForbidden)
	}
	if ErrorCodeOf(err) != ErrorCodeLifecyclePatchForbidden {
		t.Fatalf("error code = %q, want %q", ErrorCodeOf(err), ErrorCodeLifecyclePatchForbidden)
	}
	if got != task {
		t.Fatalf("rejected patch changed task: got %#v, want %#v", got, task)
	}
}

func TestOrdinaryTaskPatchPreservesLifecycle(t *testing.T) {
	t.Parallel()

	task := TaskDefinition{Title: "before", Description: "old", Priority: 1, LifecycleStatus: TaskLifecycleActive}
	got, err := PatchTaskDefinition(task, TaskPatch{Title: "after", Description: "new", Priority: 2})
	if err != nil {
		t.Fatalf("PatchTaskDefinition() error = %v", err)
	}
	if got.Title != "after" || got.Description != "new" || got.Priority != 2 {
		t.Fatalf("ordinary attributes were not patched: %#v", got)
	}
	if got.LifecycleStatus != TaskLifecycleActive {
		t.Fatalf("lifecycle changed to %q", got.LifecycleStatus)
	}
}
