package taskdomain

import (
	"errors"
	"testing"
	"time"
)

func TestExecutionLogCapturesImmutableOccurrenceAfterImage(t *testing.T) {
	t.Parallel()

	ctx := validExecutionTransition()
	current := occurrenceInStatus(ExecutionStatusActive, ctx.At.Add(-time.Hour))
	updated, log, err := BlockOccurrence(current, "waiting for review", "ping reviewer", ctx)
	if err != nil {
		t.Fatalf("BlockOccurrence() error = %v", err)
	}

	if log.ID() != ctx.LogID || log.WorkspaceID() != current.WorkspaceID || log.OccurrenceID() != current.ID {
		t.Fatalf("log identity did not capture occurrence: id=%q workspace=%q occurrence=%q", log.ID(), log.WorkspaceID(), log.OccurrenceID())
	}
	if log.ActorID() != ctx.ActorID || !log.CreatedAt().Equal(ctx.At) {
		t.Fatalf("log audit = actor %q at %v", log.ActorID(), log.CreatedAt())
	}
	if log.OccurrenceRevision() != updated.Revision {
		t.Fatalf("log revision = %d, want %d", log.OccurrenceRevision(), updated.Revision)
	}

	updated.ExecutionStatus = ExecutionStatusCancelled
	updated.BlockedReason = "changed later"
	updated.NextAction = "changed later"
	updated.Revision++
	if log.ToStatus() != ExecutionStatusBlocked || log.BlockedReason() != "waiting for review" || log.NextAction() != "ping reviewer" {
		t.Fatalf("log changed with occurrence mutation: status=%q reason=%q next=%q", log.ToStatus(), log.BlockedReason(), log.NextAction())
	}
	if log.OccurrenceRevision() != 8 {
		t.Fatalf("log revision changed to %d", log.OccurrenceRevision())
	}
}

func TestEverySuccessfulOccurrenceTransitionProducesExecutionLog(t *testing.T) {
	t.Parallel()

	ctx := validExecutionTransition()
	current := occurrenceInStatus(ExecutionStatusOpen, ctx.At)
	steps := []func(Occurrence, ExecutionTransition) (Occurrence, ExecutionLog, error){
		StartOccurrence,
		func(o Occurrence, transition ExecutionTransition) (Occurrence, ExecutionLog, error) {
			return BlockOccurrence(o, "blocked", "retry", transition)
		},
		UnblockOccurrence,
		CompleteOccurrence,
		ReopenOccurrence,
		CancelOccurrence,
		ReopenOccurrence,
		SkipOccurrence,
		ReopenOccurrence,
	}

	for i, step := range steps {
		ctx.LogID = "log-" + string(rune('a'+i))
		ctx.At = ctx.At.Add(time.Minute)
		before := current
		var log ExecutionLog
		var err error
		current, log, err = step(current, ctx)
		if err != nil {
			t.Fatalf("step %d error = %v", i, err)
		}
		if log.IsZero() {
			t.Fatalf("step %d produced zero log", i)
		}
		if log.FromStatus() != before.ExecutionStatus || log.ToStatus() != current.ExecutionStatus {
			t.Fatalf("step %d log edge = %q -> %q, want %q -> %q", i, log.FromStatus(), log.ToStatus(), before.ExecutionStatus, current.ExecutionStatus)
		}
		if log.OccurrenceRevision() != current.Revision {
			t.Fatalf("step %d log revision = %d, want %d", i, log.OccurrenceRevision(), current.Revision)
		}
	}
}

func TestExecutionTransitionAuditFieldsAreRequired(t *testing.T) {
	t.Parallel()

	current := occurrenceInStatus(ExecutionStatusOpen, time.Time{})
	valid := validExecutionTransition()
	for _, tc := range []struct {
		name string
		ctx  ExecutionTransition
	}{
		{name: "missing log id", ctx: ExecutionTransition{ActorID: valid.ActorID, At: valid.At}},
		{name: "blank log id", ctx: ExecutionTransition{LogID: " ", ActorID: valid.ActorID, At: valid.At}},
		{name: "missing actor", ctx: ExecutionTransition{LogID: valid.LogID, At: valid.At}},
		{name: "blank actor", ctx: ExecutionTransition{LogID: valid.LogID, ActorID: "\t", At: valid.At}},
		{name: "missing timestamp", ctx: ExecutionTransition{LogID: valid.LogID, ActorID: valid.ActorID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, log, err := StartOccurrence(current, tc.ctx)
			if !errors.Is(err, ErrInvalidExecutionLog) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidExecutionLog)
			}
			if got != current || !log.IsZero() {
				t.Fatalf("invalid audit fields changed state: occurrence=%#v log=%#v", got, log)
			}
		})
	}
}

func TestExecutionLogDoneAfterImageCapturesCompletedAt(t *testing.T) {
	t.Parallel()

	ctx := validExecutionTransition()
	_, log, err := CompleteOccurrence(occurrenceInStatus(ExecutionStatusOpen, time.Time{}), ctx)
	if err != nil {
		t.Fatalf("CompleteOccurrence() error = %v", err)
	}
	completedAt, ok := log.CompletedAt()
	if !ok || !completedAt.Equal(ctx.At) {
		t.Fatalf("log completed_at = (%v, %v), want (%v, true)", completedAt, ok, ctx.At)
	}

	_, reopenedLog, err := ReopenOccurrence(occurrenceInStatus(ExecutionStatusDone, ctx.At), ExecutionTransition{LogID: "log-2", ActorID: ctx.ActorID, At: ctx.At.Add(time.Minute)})
	if err != nil {
		t.Fatalf("ReopenOccurrence() error = %v", err)
	}
	if completedAt, ok := reopenedLog.CompletedAt(); ok || !completedAt.IsZero() {
		t.Fatalf("reopen log completed_at = (%v, %v), want zero false", completedAt, ok)
	}
}
