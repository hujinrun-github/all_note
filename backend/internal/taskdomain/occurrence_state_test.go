package taskdomain

import (
	"errors"
	"testing"
	"time"
)

func TestOccurrenceExecutionStateMachine(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 22, 9, 30, 0, 0, time.UTC)
	ctx := ExecutionTransition{LogID: "log-1", ActorID: "user-1", At: now}

	type command struct {
		name string
		to   ExecutionStatus
		act  func(Occurrence) (Occurrence, ExecutionLog, error)
	}
	commands := []command{
		{name: "start", to: ExecutionStatusActive, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return StartOccurrence(o, ctx)
		}},
		{name: "block", to: ExecutionStatusBlocked, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return BlockOccurrence(o, "waiting for review", "ask the reviewer", ctx)
		}},
		{name: "unblock", to: ExecutionStatusActive, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return UnblockOccurrence(o, ctx)
		}},
		{name: "complete", to: ExecutionStatusDone, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return CompleteOccurrence(o, ctx)
		}},
		{name: "skip", to: ExecutionStatusSkipped, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return SkipOccurrence(o, ctx)
		}},
		{name: "cancel", to: ExecutionStatusCancelled, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return CancelOccurrence(o, ctx)
		}},
		{name: "reopen", to: ExecutionStatusOpen, act: func(o Occurrence) (Occurrence, ExecutionLog, error) {
			return ReopenOccurrence(o, ctx)
		}},
	}

	allowed := map[ExecutionStatus]map[string]bool{
		ExecutionStatusOpen: {
			"start": true, "complete": true, "skip": true, "cancel": true,
		},
		ExecutionStatusActive: {
			"block": true, "complete": true, "cancel": true,
		},
		ExecutionStatusBlocked: {
			"unblock": true, "cancel": true,
		},
		ExecutionStatusDone:      {"reopen": true},
		ExecutionStatusSkipped:   {"reopen": true},
		ExecutionStatusCancelled: {"reopen": true},
	}

	for _, from := range allExecutionStatuses() {
		for _, cmd := range commands {
			from, cmd := from, cmd
			t.Run(string(from)+"/"+cmd.name, func(t *testing.T) {
				current := occurrenceInStatus(from, now)
				got, log, err := cmd.act(current)

				if !allowed[from][cmd.name] {
					if !errors.Is(err, ErrInvalidOccurrenceTransition) {
						t.Fatalf("error = %v, want %v", err, ErrInvalidOccurrenceTransition)
					}
					if got != current {
						t.Fatalf("rejected transition mutated occurrence: got %#v, want %#v", got, current)
					}
					if !log.IsZero() {
						t.Fatalf("rejected transition produced log: %#v", log)
					}
					return
				}

				if err != nil {
					t.Fatalf("transition error = %v", err)
				}
				if got.ExecutionStatus != cmd.to {
					t.Fatalf("status = %q, want %q", got.ExecutionStatus, cmd.to)
				}
				if got.Revision != current.Revision+1 {
					t.Fatalf("revision = %d, want %d", got.Revision, current.Revision+1)
				}
				if log.FromStatus() != from || log.ToStatus() != cmd.to {
					t.Fatalf("log edge = %q -> %q, want %q -> %q", log.FromStatus(), log.ToStatus(), from, cmd.to)
				}
			})
		}
	}
}

func TestOccurrenceBlockRequiresReasonAndNextAction(t *testing.T) {
	t.Parallel()

	current := occurrenceInStatus(ExecutionStatusActive, time.Time{})
	ctx := validExecutionTransition()
	for _, tc := range []struct {
		name       string
		reason     string
		nextAction string
	}{
		{name: "missing reason", nextAction: "call owner"},
		{name: "blank reason", reason: " \t", nextAction: "call owner"},
		{name: "missing next action", reason: "dependency unavailable"},
		{name: "blank next action", reason: "dependency unavailable", nextAction: "\n "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, log, err := BlockOccurrence(current, tc.reason, tc.nextAction, ctx)
			if !errors.Is(err, ErrBlockedDetailsRequired) {
				t.Fatalf("error = %v, want %v", err, ErrBlockedDetailsRequired)
			}
			if got != current || !log.IsZero() {
				t.Fatalf("rejected block changed state: occurrence=%#v log=%#v", got, log)
			}
		})
	}
}

func TestOccurrenceBlockedMetadataIsCurrentSnapshot(t *testing.T) {
	t.Parallel()

	current := occurrenceInStatus(ExecutionStatusActive, time.Time{})
	blocked, log, err := BlockOccurrence(current, "  dependency unavailable  ", "  call owner  ", validExecutionTransition())
	if err != nil {
		t.Fatalf("BlockOccurrence() error = %v", err)
	}
	if blocked.BlockedReason != "dependency unavailable" || blocked.NextAction != "call owner" {
		t.Fatalf("blocked snapshot = (%q, %q)", blocked.BlockedReason, blocked.NextAction)
	}
	if log.BlockedReason() != blocked.BlockedReason || log.NextAction() != blocked.NextAction {
		t.Fatalf("log did not capture blocked after-image")
	}

	unblocked, _, err := UnblockOccurrence(blocked, ExecutionTransition{LogID: "log-2", ActorID: "user-1", At: validExecutionTransition().At.Add(time.Minute)})
	if err != nil {
		t.Fatalf("UnblockOccurrence() error = %v", err)
	}
	if unblocked.BlockedReason != "" || unblocked.NextAction != "" {
		t.Fatalf("unblock retained blocked snapshot: %#v", unblocked)
	}
}

func TestOccurrenceDoneAndCompletedAtAreBidirectionallyConsistent(t *testing.T) {
	t.Parallel()

	ctx := validExecutionTransition()
	done, _, err := CompleteOccurrence(occurrenceInStatus(ExecutionStatusOpen, time.Time{}), ctx)
	if err != nil {
		t.Fatalf("CompleteOccurrence() error = %v", err)
	}
	if done.CompletedAt == nil || !done.CompletedAt.Equal(ctx.At) {
		t.Fatalf("completed_at = %v, want %v", done.CompletedAt, ctx.At)
	}

	reopened, _, err := ReopenOccurrence(done, ExecutionTransition{LogID: "log-2", ActorID: ctx.ActorID, At: ctx.At.Add(time.Minute)})
	if err != nil {
		t.Fatalf("ReopenOccurrence() error = %v", err)
	}
	if reopened.CompletedAt != nil {
		t.Fatalf("reopened completed_at = %v, want nil", reopened.CompletedAt)
	}

	completedAt := ctx.At
	for _, invalid := range []Occurrence{
		{ID: "occ-1", WorkspaceID: "workspace-1", ExecutionStatus: ExecutionStatusDone, CompletedAt: nil},
		{ID: "occ-1", WorkspaceID: "workspace-1", ExecutionStatus: ExecutionStatusOpen, CompletedAt: &completedAt},
	} {
		got, log, err := CancelOccurrence(invalid, ctx)
		if !errors.Is(err, ErrInvalidOccurrenceSnapshot) {
			t.Fatalf("CancelOccurrence(%#v) error = %v, want %v", invalid, err, ErrInvalidOccurrenceSnapshot)
		}
		if got != invalid || !log.IsZero() {
			t.Fatalf("invalid snapshot was mutated: occurrence=%#v log=%#v", got, log)
		}
	}
}

func TestSingleOccurrenceCannotBeSkipped(t *testing.T) {
	t.Parallel()

	current := occurrenceInStatus(ExecutionStatusOpen, time.Time{})
	current.Recurring = false
	got, log, err := SkipOccurrence(current, validExecutionTransition())
	if !errors.Is(err, ErrSingleOccurrenceCannotSkip) {
		t.Fatalf("SkipOccurrence() error = %v, want %v", err, ErrSingleOccurrenceCannotSkip)
	}
	if got != current || !log.IsZero() {
		t.Fatalf("rejected skip changed state: occurrence=%#v log=%#v", got, log)
	}
}

func TestOccurrenceTerminalStatesRequireExplicitReopen(t *testing.T) {
	t.Parallel()

	for _, status := range []ExecutionStatus{ExecutionStatusDone, ExecutionStatusSkipped, ExecutionStatusCancelled} {
		current := occurrenceInStatus(status, time.Time{})
		got, _, err := StartOccurrence(current, validExecutionTransition())
		if !errors.Is(err, ErrInvalidOccurrenceTransition) || got != current {
			t.Fatalf("StartOccurrence(%q) = (%#v, %v), want unchanged invalid transition", status, got, err)
		}

		reopened, _, err := ReopenOccurrence(current, validExecutionTransition())
		if err != nil {
			t.Fatalf("ReopenOccurrence(%q) error = %v", status, err)
		}
		if reopened.ExecutionStatus != ExecutionStatusOpen {
			t.Fatalf("ReopenOccurrence(%q) status = %q", status, reopened.ExecutionStatus)
		}
	}
}

func allExecutionStatuses() []ExecutionStatus {
	return []ExecutionStatus{
		ExecutionStatusOpen,
		ExecutionStatusActive,
		ExecutionStatusBlocked,
		ExecutionStatusDone,
		ExecutionStatusSkipped,
		ExecutionStatusCancelled,
	}
}

func occurrenceInStatus(status ExecutionStatus, fallback time.Time) Occurrence {
	if fallback.IsZero() {
		fallback = validExecutionTransition().At
	}
	o := Occurrence{
		WorkspaceID:     "workspace-1",
		ID:              "occurrence-1",
		TaskID:          "task-1",
		OccurrenceKey:   "2026-07-22",
		ExecutionStatus: status,
		Recurring:       true,
		Revision:        7,
	}
	if status == ExecutionStatusActive || status == ExecutionStatusBlocked {
		o.ActualStartAt = &fallback
	}
	if status == ExecutionStatusBlocked {
		o.BlockedReason = "waiting"
		o.NextAction = "follow up"
	}
	if status == ExecutionStatusDone {
		o.CompletedAt = &fallback
	}
	return o
}

func validExecutionTransition() ExecutionTransition {
	return ExecutionTransition{
		LogID:   "log-1",
		ActorID: "user-1",
		At:      time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
	}
}
