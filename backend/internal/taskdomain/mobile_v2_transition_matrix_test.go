package taskdomain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mobileV2TransitionMatrix struct {
	Project    []mobileV2TransitionCase `json:"project"`
	Task       []mobileV2TransitionCase `json:"task"`
	Occurrence []mobileV2TransitionCase `json:"occurrence"`
}

type mobileV2TransitionCase struct {
	Name                   string `json:"name"`
	From                   string `json:"from"`
	ArchivedFrom           string `json:"archived_from"`
	Action                 string `json:"action"`
	NonTerminalOccurrences int    `json:"non_terminal_occurrences"`
	Recurring              bool   `json:"recurring"`
	Reason                 string `json:"reason"`
	NextAction             string `json:"next_action"`
	Outcome                string `json:"outcome"`
	To                     string `json:"to"`
	Error                  string `json:"error"`
}

func TestMTDV2DomainTransitionMatrixMatchesFrozenFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "transition-matrix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var matrix mobileV2TransitionMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatal(err)
	}
	for _, test := range matrix.Project {
		t.Run("project/"+test.Name, func(t *testing.T) {
			got, err := applyMobileV2ProjectFixture(test)
			assertMobileV2TransitionFixture(t, string(got.Status), err, test)
		})
	}
	for _, test := range matrix.Task {
		t.Run("task/"+test.Name, func(t *testing.T) {
			got, err := applyMobileV2TaskFixture(test)
			assertMobileV2TransitionFixture(t, string(got), err, test)
		})
	}
	for _, test := range matrix.Occurrence {
		t.Run("occurrence/"+test.Name, func(t *testing.T) {
			got, err := applyMobileV2OccurrenceFixture(test)
			assertMobileV2TransitionFixture(t, string(got.ExecutionStatus), err, test)
		})
	}
}

func applyMobileV2ProjectFixture(test mobileV2TransitionCase) (Project, error) {
	project := Project{Status: ProjectStatus(test.From)}
	if test.ArchivedFrom != "" {
		status := ProjectStatus(test.ArchivedFrom)
		project.ArchivedFromStatus = &status
	}
	switch test.Action {
	case "activate":
		return ActivateProject(project)
	case "pause":
		return PauseProject(project)
	case "resume":
		return ResumeProject(project)
	case "complete":
		return CompleteProject(project, test.NonTerminalOccurrences)
	case "archive":
		return ArchiveProject(project)
	case "restore":
		return RestoreProject(project, nil)
	default:
		return project, ErrInvalidProjectTransition
	}
}

func applyMobileV2TaskFixture(test mobileV2TransitionCase) (TaskLifecycleStatus, error) {
	current := TaskLifecycleStatus(test.From)
	switch test.Action {
	case "publish":
		return PublishTask(current)
	case "pause":
		return PauseTask(current)
	case "resume":
		return ResumeTask(current)
	case "complete":
		return CompleteTask(current)
	case "cancel":
		return CancelTask(current)
	case "restore":
		return RestoreTask(current)
	case "archive":
		return ArchiveTask(current)
	default:
		return current, ErrInvalidTaskTransition
	}
}

func applyMobileV2OccurrenceFixture(test mobileV2TransitionCase) (Occurrence, error) {
	at := time.Date(2026, 7, 23, 16, 5, 4, 123000000, time.UTC)
	current := Occurrence{
		ID: "occurrence-1", TaskID: "task-1", OccurrenceKey: "key-1",
		ExecutionStatus: ExecutionStatus(test.From), Recurring: test.Recurring, Revision: 1,
	}
	if current.ExecutionStatus == ExecutionStatusDone {
		current.CompletedAt = &at
	}
	if current.ExecutionStatus == ExecutionStatusBlocked {
		current.BlockedReason = test.Reason
		current.NextAction = test.NextAction
	}
	transition := ExecutionTransition{LogID: "log-1", ActorID: "actor-1", At: at}
	var updated Occurrence
	var err error
	switch test.Action {
	case "start":
		updated, _, err = StartOccurrence(current, transition)
	case "block":
		updated, _, err = BlockOccurrence(current, test.Reason, test.NextAction, transition)
	case "unblock":
		updated, _, err = UnblockOccurrence(current, transition)
	case "complete":
		updated, _, err = CompleteOccurrence(current, transition)
	case "skip":
		updated, _, err = SkipOccurrence(current, transition)
	case "cancel":
		updated, _, err = CancelOccurrence(current, transition)
	case "reopen":
		updated, _, err = ReopenOccurrence(current, transition)
	default:
		return current, ErrInvalidOccurrenceTransition
	}
	return updated, err
}

func assertMobileV2TransitionFixture(t *testing.T, status string, err error, test mobileV2TransitionCase) {
	t.Helper()
	if test.Outcome == "applied" {
		if err != nil || status != test.To {
			t.Fatalf("status=%q err=%v, want %q", status, err, test.To)
		}
		return
	}
	if got := string(ErrorCodeOf(err)); got != test.Error {
		t.Fatalf("error code=%q err=%v, want %q", got, err, test.Error)
	}
}
