package taskruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestTaskAdapterNudgesAfterRecurringCreateCommit(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	nudger := &generationNudgerStub{err: errors.New("control store temporarily unavailable")}
	adapter := taskServiceAdapter{delegate: taskServiceDelegateStub{}, generation: nudger}
	_, err := adapter.CreateTask(t.Context(), taskdomain.CreateTaskRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 7, At: now,
		Snapshot: taskdomain.TaskAggregateSnapshot{Versions: []taskdomain.ScheduleVersion{{RecurrenceType: taskdomain.RecurrenceDaily}}},
	})
	if err != nil {
		t.Fatalf("post-commit nudge failure changed command result: %v", err)
	}
	if len(nudger.calls) != 1 || nudger.calls[0].workspaceID != "workspace-1" || nudger.calls[0].epoch != 7 || !nudger.calls[0].at.Equal(now) {
		t.Fatalf("nudge calls=%#v", nudger.calls)
	}
}

func TestTaskAdapterDoesNotNudgeOneTimeCreate(t *testing.T) {
	nudger := &generationNudgerStub{}
	adapter := taskServiceAdapter{delegate: taskServiceDelegateStub{}, generation: nudger}
	_, err := adapter.CreateTask(t.Context(), taskdomain.CreateTaskRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 7, At: time.Now(),
		Snapshot: taskdomain.TaskAggregateSnapshot{Versions: []taskdomain.ScheduleVersion{{RecurrenceType: taskdomain.RecurrenceNone}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(nudger.calls) != 0 {
		t.Fatalf("one-time task nudges=%#v", nudger.calls)
	}
}

func TestScheduleAdapterNudgesAfterRuleChangeCommit(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	nudger := &generationNudgerStub{}
	adapter := scheduleServiceAdapter{delegate: scheduleServiceDelegateStub{}, generation: nudger}
	_, err := adapter.RescheduleThisAndFollowing(t.Context(), taskdomain.RescheduleThisAndFutureRequest{
		WorkspaceID: "workspace-1", ExpectedRuntimeEpoch: 8,
	}, taskapp.CommandMetadata{ActorID: "user-1", CommandID: "command-1", At: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(nudger.calls) != 1 || nudger.calls[0].workspaceID != "workspace-1" || nudger.calls[0].epoch != 8 {
		t.Fatalf("nudge calls=%#v", nudger.calls)
	}
}

type generationNudgeCall struct {
	workspaceID string
	epoch       int64
	at          time.Time
}

type generationNudgerStub struct {
	calls []generationNudgeCall
	err   error
}

func (s *generationNudgerStub) Nudge(_ context.Context, workspaceID string, epoch int64, at time.Time) error {
	s.calls = append(s.calls, generationNudgeCall{workspaceID: workspaceID, epoch: epoch, at: at})
	return s.err
}

type taskServiceDelegateStub struct{ err error }

func (s taskServiceDelegateStub) CreateTask(context.Context, taskdomain.CreateTaskRequest) (taskdomain.TaskCommandResult, error) {
	return taskdomain.TaskCommandResult{}, s.err
}
func (s taskServiceDelegateStub) PatchTask(context.Context, taskdomain.PatchTaskRequest) (taskdomain.TaskCommandResult, error) {
	return taskdomain.TaskCommandResult{}, s.err
}
func (s taskServiceDelegateStub) ExecuteLifecycleCommand(context.Context, taskdomain.LifecycleCommandRequest) (taskdomain.TaskCommandResult, error) {
	return taskdomain.TaskCommandResult{}, s.err
}

type scheduleServiceDelegateStub struct{ err error }

func (s scheduleServiceDelegateStub) RescheduleOccurrence(context.Context, taskdomain.RescheduleOccurrenceRequest) (taskdomain.ScheduleCommandResult, error) {
	return taskdomain.ScheduleCommandResult{}, s.err
}
func (s scheduleServiceDelegateStub) RescheduleThisAndFuture(context.Context, taskdomain.RescheduleThisAndFutureRequest) (taskdomain.ScheduleCommandResult, error) {
	return taskdomain.ScheduleCommandResult{}, s.err
}
