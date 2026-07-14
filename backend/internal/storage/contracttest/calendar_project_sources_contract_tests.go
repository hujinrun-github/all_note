package contracttest

import (
	"context"
	"testing"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func RunCalendarProjectSourcesSuite(t *testing.T, factory StoreFactory) {
	t.Helper()

	t.Run("ProjectSourcesListSaveAndScopeByUser", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedContractContext(t, store)
		workspaceID := contractWorkspaceID(t)
		ownerID := workspaceID + "_owner"
		ctx = auth.ContextWithIdentity(ctx, auth.RequestIdentity{UserID: ownerID, WorkspaceID: workspaceID, Role: "owner"})

		regular, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Client Work", Type: "regular"})
		if err != nil {
			t.Fatalf("create regular project: %v", err)
		}
		learning, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Rust Learning", Type: "learning"})
		if err != nil {
			t.Fatalf("create learning project: %v", err)
		}

		initial, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list initial project sources: %v", err)
		}
		if !hasCalendarSource(initial.Sources, "personal", true, true) {
			t.Fatalf("expected personal default source, got %+v", initial)
		}
		if !hasCalendarSource(initial.AvailableProjects, regular.ID, false, false) ||
			!hasCalendarSource(initial.AvailableProjects, learning.ID, false, false) {
			t.Fatalf("expected unconfigured projects to be available, got %+v", initial)
		}

		saveResponse, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: "personal", Enabled: false, Color: "#000000", OrderIndex: 0},
			{ProjectID: regular.ID, Enabled: true, Color: "#ff0000", OrderIndex: 2},
			{ProjectID: learning.ID, Enabled: false, Color: "#00ff00", OrderIndex: 3},
		})
		if err != nil {
			t.Fatalf("save project sources: %v", err)
		}
		if !hasCalendarSource(saveResponse.Sources, "personal", true, true) ||
			!hasCalendarSource(saveResponse.Sources, regular.ID, true, false) ||
			!hasCalendarSourceConfig(saveResponse.Sources, regular.ID, "#ff0000", 2) ||
			!hasCalendarSource(saveResponse.AvailableProjects, learning.ID, false, false) ||
			!hasCalendarSourceConfig(saveResponse.AvailableProjects, learning.ID, "#00ff00", 3) {
			t.Fatalf("expected save to return refreshed canonical response, got %+v", saveResponse)
		}

		saved, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list saved project sources: %v", err)
		}
		if !hasCalendarSource(saved.Sources, "personal", true, true) {
			t.Fatalf("expected personal to remain default despite save input, got %+v", saved)
		}
		if !hasCalendarSource(saved.Sources, regular.ID, true, false) {
			t.Fatalf("expected enabled regular project in sources, got %+v", saved)
		}
		if !hasCalendarSourceConfig(saved.Sources, regular.ID, "#ff0000", 2) {
			t.Fatalf("expected enabled regular project config to round-trip, got %+v", saved)
		}
		if !hasCalendarSource(saved.AvailableProjects, learning.ID, false, false) {
			t.Fatalf("expected disabled learning project in available projects, got %+v", saved)
		}
		if !hasCalendarSourceConfig(saved.AvailableProjects, learning.ID, "#00ff00", 3) {
			t.Fatalf("expected disabled learning project config to round-trip, got %+v", saved)
		}

		memberID := workspaceID + "_member"
		seedContractWorkspaceMember(t, store, workspaceID, memberID)
		memberCtx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
		memberCtx = auth.ContextWithIdentity(memberCtx, auth.RequestIdentity{UserID: memberID, WorkspaceID: workspaceID, Role: "member"})

		memberSources, err := store.Calendar().ListProjectSources(memberCtx)
		if err != nil {
			t.Fatalf("list member project sources: %v", err)
		}
		if hasCalendarSource(memberSources.Sources, regular.ID, true, false) {
			t.Fatalf("member inherited owner enabled project: %+v", memberSources)
		}
		if !hasCalendarSource(memberSources.AvailableProjects, regular.ID, false, false) {
			t.Fatalf("expected member to see regular project as available, got %+v", memberSources)
		}
	})

	t.Run("ProjectSourcesRejectInvalidIDWithoutPartialSave", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedCalendarContractContext(t, store)
		regular, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Existing Source", Type: "regular"})
		if err != nil {
			t.Fatalf("create regular project: %v", err)
		}
		if _, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: regular.ID, Enabled: true, Color: "#123456", OrderIndex: 4},
		}); err != nil {
			t.Fatalf("seed enabled project source: %v", err)
		}

		if _, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: "missing-project", Enabled: true, Color: "#abcdef", OrderIndex: 9},
		}); err == nil {
			t.Fatalf("expected invalid project id to return an error")
		}

		saved, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list project sources after invalid save: %v", err)
		}
		if !hasCalendarSource(saved.Sources, regular.ID, true, false) ||
			!hasCalendarSourceConfig(saved.Sources, regular.ID, "#123456", 4) {
			t.Fatalf("expected existing valid source to remain unchanged, got %+v", saved)
		}
		if hasCalendarSource(saved.Sources, "missing-project", true, false) ||
			hasCalendarSource(saved.AvailableProjects, "missing-project", true, false) {
			t.Fatalf("invalid project id was persisted: %+v", saved)
		}
	})

	t.Run("ProjectSourcesRejectDisallowedProjectType", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		runner, ok := store.(contractSQLRunner)
		if !ok {
			t.Fatalf("store %T does not expose SQL runner", store)
		}
		ctx := scopedCalendarContractContext(t, store)
		workspaceID := contractWorkspaceID(t)
		projectID := "disallowed-calendar-source"

		stmt := `
			INSERT INTO task_projects (id, workspace_id, name, type, description, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', unixepoch(), unixepoch())
		`
		if store.Capabilities().TimeRanges {
			stmt = `
				INSERT INTO task_projects (id, workspace_id, name, type, description, created_at, updated_at)
				VALUES ($1, $2, $3, $4, '', now(), now())
			`
		}
		if _, err := runner.ExecContext(ctx, stmt, projectID, workspaceID, "Personal Source", "personal"); err != nil {
			t.Fatalf("seed disallowed project type: %v", err)
		}

		if _, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: projectID, Enabled: true, Color: "#abcdef", OrderIndex: 8},
		}); err == nil {
			t.Fatalf("expected disallowed project type to return an error")
		}

		saved, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list project sources after disallowed save: %v", err)
		}
		if hasCalendarSourceID(saved.Sources, projectID) ||
			hasCalendarSourceID(saved.AvailableProjects, projectID) {
			t.Fatalf("disallowed project type was visible as a calendar source: %+v", saved)
		}
	})

	t.Run("ProjectSourcesRejectCrossWorkspaceID", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedCalendarContractContext(t, store)
		otherWorkspaceID := contractWorkspaceID(t) + "_other"
		otherCtx := seedWorkspaceDefaults(t, store, otherWorkspaceID)
		otherCtx = auth.ContextWithIdentity(otherCtx, auth.RequestIdentity{UserID: otherWorkspaceID + "_owner", WorkspaceID: otherWorkspaceID, Role: "owner"})
		otherProject, err := store.Tasks().CreateProject(otherCtx, &model.CreateTaskProjectRequest{Name: "Other Workspace Source", Type: "regular"})
		if err != nil {
			t.Fatalf("create other workspace project: %v", err)
		}

		if _, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: otherProject.ID, Enabled: true, Color: "#654321", OrderIndex: 5},
		}); err == nil {
			t.Fatalf("expected cross-workspace project id to return an error")
		}
	})

	t.Run("ProjectSourcesAreIsolatedAcrossWorkspaces", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		workspaceAID := contractWorkspaceID(t) + "_a"
		ctxA := seedWorkspaceDefaults(t, store, workspaceAID)
		ctxA = auth.ContextWithIdentity(ctxA, auth.RequestIdentity{UserID: workspaceAID + "_owner", WorkspaceID: workspaceAID, Role: "owner"})
		projectA, err := store.Tasks().CreateProject(ctxA, &model.CreateTaskProjectRequest{Name: "Workspace A Source", Type: "regular"})
		if err != nil {
			t.Fatalf("create workspace A project: %v", err)
		}
		if _, err := store.Calendar().SaveProjectSources(ctxA, []model.CalendarProjectSourceInput{
			{ProjectID: projectA.ID, Enabled: true, Color: "#aa0000", OrderIndex: 1},
		}); err != nil {
			t.Fatalf("enable workspace A project source: %v", err)
		}

		workspaceBID := contractWorkspaceID(t) + "_b"
		ctxB := seedWorkspaceDefaults(t, store, workspaceBID)
		ctxB = auth.ContextWithIdentity(ctxB, auth.RequestIdentity{UserID: workspaceBID + "_owner", WorkspaceID: workspaceBID, Role: "owner"})

		if _, err := store.Calendar().SaveProjectSources(ctxB, []model.CalendarProjectSourceInput{
			{ProjectID: projectA.ID, Enabled: true, Color: "#00aa00", OrderIndex: 2},
		}); err == nil {
			t.Fatalf("expected workspace B to be unable to save workspace A project source")
		}

		sourcesB, err := store.Calendar().ListProjectSources(ctxB)
		if err != nil {
			t.Fatalf("list workspace B project sources: %v", err)
		}
		if hasCalendarSourceID(sourcesB.Sources, projectA.ID) ||
			hasCalendarSourceID(sourcesB.AvailableProjects, projectA.ID) {
			t.Fatalf("workspace B saw workspace A project source: %+v", sourcesB)
		}
	})

	t.Run("ProjectSourcesBatchSaveIsAtomic", func(t *testing.T) {
		store := factory(t)
		defer store.Close()

		ctx := scopedCalendarContractContext(t, store)
		regular, err := store.Tasks().CreateProject(ctx, &model.CreateTaskProjectRequest{Name: "Atomic Source", Type: "regular"})
		if err != nil {
			t.Fatalf("create regular project: %v", err)
		}

		if _, err := store.Calendar().SaveProjectSources(ctx, []model.CalendarProjectSourceInput{
			{ProjectID: regular.ID, Enabled: true, Color: "#fedcba", OrderIndex: 6},
			{ProjectID: "missing-project", Enabled: true, Color: "#abcdef", OrderIndex: 7},
		}); err == nil {
			t.Fatalf("expected batch with invalid project id to return an error")
		}

		saved, err := store.Calendar().ListProjectSources(ctx)
		if err != nil {
			t.Fatalf("list project sources after failed batch: %v", err)
		}
		if hasCalendarSource(saved.Sources, regular.ID, true, false) ||
			hasCalendarSourceConfig(saved.AvailableProjects, regular.ID, "#fedcba", 6) {
			t.Fatalf("valid row from failed batch was persisted: %+v", saved)
		}
	})
}

func scopedCalendarContractContext(t *testing.T, store storage.Store) context.Context {
	t.Helper()

	ctx := scopedContractContext(t, store)
	workspaceID := contractWorkspaceID(t)
	return auth.ContextWithIdentity(ctx, auth.RequestIdentity{UserID: workspaceID + "_owner", WorkspaceID: workspaceID, Role: "owner"})
}

func seedContractWorkspaceMember(t *testing.T, store storage.Store, workspaceID, userID string) {
	t.Helper()

	user := contractUser(userID, userID+"@example.com", userID, "user")
	if err := store.Auth().CreateUser(context.Background(), user); err != nil {
		t.Fatalf("create workspace member user: %v", err)
	}
	if err := store.Auth().AddWorkspaceMember(context.Background(), workspaceID, userID, "member"); err != nil {
		t.Fatalf("add workspace member: %v", err)
	}
}

func hasCalendarSource(sources []model.CalendarProjectSource, projectID string, enabled, isDefault bool) bool {
	for _, source := range sources {
		if source.ProjectID == projectID && source.Enabled == enabled && source.Default == isDefault {
			return true
		}
	}
	return false
}

func hasCalendarSourceID(sources []model.CalendarProjectSource, projectID string) bool {
	for _, source := range sources {
		if source.ProjectID == projectID {
			return true
		}
	}
	return false
}

func hasCalendarSourceConfig(sources []model.CalendarProjectSource, projectID, color string, orderIndex int) bool {
	for _, source := range sources {
		if source.ProjectID == projectID && source.Color == color && source.OrderIndex == orderIndex {
			return true
		}
	}
	return false
}
