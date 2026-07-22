package mobilecontract

import (
	"errors"
	"reflect"
	"testing"
)

func TestTaskDomainShutdownAffectedEntitiesAndScopesAreStable(t *testing.T) {
	wantEntities := []MobileEntity{
		MobileEntityEvent,
		MobileEntityProject,
		MobileEntityTask,
		MobileEntityTaskOccurrence,
		MobileEntityTaskRecurrenceRule,
		MobileEntityWatchTaskProjection,
	}
	if got := AffectedTaskDomainEntities(); !reflect.DeepEqual(got, wantEntities) {
		t.Fatalf("affected entities = %v, want %v", got, wantEntities)
	}

	wantScopes := []MobileOperationScope{
		MobileScopeChanges,
		MobileScopeMutation,
		MobileScopeSnapshot,
		MobileScopeWatch,
	}
	if got := RequiredTaskDomainShutdownScopes(); !reflect.DeepEqual(got, wantScopes) {
		t.Fatalf("required scopes = %v, want %v", got, wantScopes)
	}

	// Callers must not be able to mutate the package-level inventories.
	entities := AffectedTaskDomainEntities()
	entities[0] = MobileEntityNote
	if got := AffectedTaskDomainEntities()[0]; got != MobileEntityEvent {
		t.Fatalf("affected entity inventory was mutated: %q", got)
	}
	scopes := RequiredTaskDomainShutdownScopes()
	scopes[0] = "invalid"
	if got := RequiredTaskDomainShutdownScopes()[0]; got != MobileScopeChanges {
		t.Fatalf("required scope inventory was mutated: %q", got)
	}
}

func TestTaskDomainShutdownPreflightRequiresEveryScopeInDeterministicOrder(t *testing.T) {
	shutdown, err := NewTaskDomainShutdown("workspace-a", 7)
	if err != nil {
		t.Fatalf("new shutdown: %v", err)
	}

	assertMissingTaskDomainShutdownScopes(t, shutdown.Preflight(), []MobileOperationScope{
		MobileScopeChanges,
		MobileScopeMutation,
		MobileScopeSnapshot,
		MobileScopeWatch,
	})

	for _, scope := range []MobileOperationScope{MobileScopeWatch, MobileScopeMutation} {
		changed, closeErr := shutdown.CloseScope(scope)
		if closeErr != nil || !changed {
			t.Fatalf("close %q: changed=%v err=%v", scope, changed, closeErr)
		}
	}
	assertMissingTaskDomainShutdownScopes(t, shutdown.Preflight(), []MobileOperationScope{
		MobileScopeChanges,
		MobileScopeSnapshot,
	})

	for _, scope := range []MobileOperationScope{MobileScopeChanges, MobileScopeSnapshot} {
		if changed, closeErr := shutdown.CloseScope(scope); closeErr != nil || !changed {
			t.Fatalf("close %q: changed=%v err=%v", scope, changed, closeErr)
		}
	}
	if err := shutdown.Preflight(); err != nil {
		t.Fatalf("completed preflight: %v", err)
	}
}

func TestTaskDomainShutdownClosingAScopeIsIdempotent(t *testing.T) {
	shutdown, err := NewTaskDomainShutdown("workspace-a", 9)
	if err != nil {
		t.Fatalf("new shutdown: %v", err)
	}
	if changed, err := shutdown.CloseScope(MobileScopeSnapshot); err != nil || !changed {
		t.Fatalf("first close: changed=%v err=%v", changed, err)
	}
	wantRevision := shutdown.Revision()
	if changed, err := shutdown.CloseScope(MobileScopeSnapshot); err != nil || changed {
		t.Fatalf("repeated close: changed=%v err=%v", changed, err)
	}
	if got := shutdown.Revision(); got != wantRevision {
		t.Fatalf("revision after idempotent close = %d, want %d", got, wantRevision)
	}
}

func TestTaskDomainShutdownBlocksEveryAffectedEntityAndScope(t *testing.T) {
	shutdown := fullyClosedTaskDomainShutdown(t, "workspace-a", 11)

	for _, entity := range AffectedTaskDomainEntities() {
		for _, scope := range RequiredTaskDomainShutdownScopes() {
			err := shutdown.Check(TaskDomainMobileAccess{
				WorkspaceID: "workspace-a",
				Entity:      entity,
				Scope:       scope,
				Binding: MobileContractBinding{
					Kind:        MobileBindingSession,
					WorkspaceID: "workspace-a",
					Generation:  11,
				},
			})
			assertTaskDomainUpgradeRequired(t, err, "task_domain_scope_closed", 11, 11)
		}
	}
}

func TestTaskDomainShutdownLeavesKnownNonTaskCapabilitiesAvailable(t *testing.T) {
	shutdown := fullyClosedTaskDomainShutdown(t, "workspace-a", 13)

	for _, entity := range []MobileEntity{MobileEntityNote, MobileEntityVoiceNote, MobileEntityVoiceAudio} {
		for _, scope := range RequiredTaskDomainShutdownScopes() {
			err := shutdown.Check(TaskDomainMobileAccess{
				WorkspaceID: "workspace-a",
				Entity:      entity,
				Scope:       scope,
				// An old generation is intentionally ignored for independent capabilities.
				Binding: MobileContractBinding{Kind: MobileBindingCursor, WorkspaceID: "workspace-a", Generation: 1},
			})
			if err != nil {
				t.Fatalf("non-task entity %q scope %q was blocked: %v", entity, scope, err)
			}
		}
	}
}

func TestTaskDomainShutdownInvalidatesOldCursorSessionAndTokenGenerations(t *testing.T) {
	shutdown, err := NewTaskDomainShutdown("workspace-a", 17)
	if err != nil {
		t.Fatalf("new shutdown: %v", err)
	}

	for _, kind := range []MobileBindingKind{MobileBindingCursor, MobileBindingSession, MobileBindingToken} {
		err := shutdown.Check(TaskDomainMobileAccess{
			WorkspaceID: "workspace-a",
			Entity:      MobileEntityTask,
			Scope:       MobileScopeChanges,
			Binding: MobileContractBinding{
				Kind:        kind,
				WorkspaceID: "workspace-a",
				Generation:  16,
			},
		})
		assertTaskDomainUpgradeRequired(t, err, "contract_generation_mismatch", 17, 16)
	}
}

func TestTaskDomainShutdownInvalidInputFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		workspace  string
		generation uint64
	}{
		{name: "empty workspace", workspace: "", generation: 1},
		{name: "blank workspace", workspace: "   ", generation: 1},
		{name: "zero generation", workspace: "workspace-a", generation: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTaskDomainShutdown(tc.workspace, tc.generation); !errors.Is(err, ErrInvalidTaskDomainShutdownInput) {
				t.Fatalf("new shutdown error = %v", err)
			}
		})
	}

	shutdown, err := NewTaskDomainShutdown("workspace-a", 19)
	if err != nil {
		t.Fatalf("new shutdown: %v", err)
	}
	if _, err := shutdown.CloseScope("unknown"); !errors.Is(err, ErrInvalidTaskDomainShutdownInput) {
		t.Fatalf("invalid close scope error = %v", err)
	}

	validBinding := MobileContractBinding{Kind: MobileBindingSession, WorkspaceID: "workspace-a", Generation: 19}
	invalidRequests := []TaskDomainMobileAccess{
		{WorkspaceID: "", Entity: MobileEntityTask, Scope: MobileScopeMutation, Binding: validBinding},
		{WorkspaceID: "workspace-b", Entity: MobileEntityTask, Scope: MobileScopeMutation, Binding: validBinding},
		{WorkspaceID: "workspace-a", Entity: MobileEntityTask, Scope: "invalid", Binding: validBinding},
		{WorkspaceID: "workspace-a", Entity: "unknown", Scope: MobileScopeMutation, Binding: validBinding},
		{WorkspaceID: "workspace-a", Entity: MobileEntityTask, Scope: MobileScopeMutation, Binding: MobileContractBinding{}},
	}
	for i, request := range invalidRequests {
		if err := shutdown.Check(request); !errors.Is(err, ErrInvalidTaskDomainShutdownInput) {
			t.Fatalf("invalid request %d error = %v", i, err)
		}
	}
}

func fullyClosedTaskDomainShutdown(t *testing.T, workspaceID string, generation uint64) *TaskDomainShutdown {
	t.Helper()
	shutdown, err := NewTaskDomainShutdown(workspaceID, generation)
	if err != nil {
		t.Fatalf("new shutdown: %v", err)
	}
	for _, scope := range RequiredTaskDomainShutdownScopes() {
		if _, err := shutdown.CloseScope(scope); err != nil {
			t.Fatalf("close %q: %v", scope, err)
		}
	}
	return shutdown
}

func assertMissingTaskDomainShutdownScopes(t *testing.T, err error, want []MobileOperationScope) {
	t.Helper()
	var missing *MissingTaskDomainShutdownScopesError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want MissingTaskDomainShutdownScopesError", err)
	}
	if !reflect.DeepEqual(missing.MissingScopes, want) {
		t.Fatalf("missing scopes = %v, want %v", missing.MissingScopes, want)
	}
}

func assertTaskDomainUpgradeRequired(t *testing.T, err error, reason string, required, presented uint64) {
	t.Helper()
	var upgrade *TaskDomainUpgradeRequiredError
	if !errors.As(err, &upgrade) {
		t.Fatalf("error = %v, want TaskDomainUpgradeRequiredError", err)
	}
	if upgrade.Status != 426 || upgrade.Code != "mobile_task_domain_upgrade_required" ||
		upgrade.SchemaVersion != SchemaVersion || upgrade.Retryable || upgrade.Reason != reason ||
		upgrade.RequiredGeneration != required || upgrade.PresentedGeneration != presented {
		t.Fatalf("upgrade error = %+v", upgrade)
	}
}
