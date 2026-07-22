package taskdomain

import (
	"errors"
	"testing"
)

func TestProjectKindAndHorizonAreOrthogonal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		kind    ProjectKind
		horizon ProjectHorizon
	}{
		{name: "standard short", kind: ProjectKindStandard, horizon: ProjectHorizonShort},
		{name: "standard long", kind: ProjectKindStandard, horizon: ProjectHorizonLong},
		{name: "learning short", kind: ProjectKindLearning, horizon: ProjectHorizonShort},
		{name: "learning long", kind: ProjectKindLearning, horizon: ProjectHorizonLong},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := Project{
				WorkspaceID: "workspace-a",
				ID:          "project-1",
				Name:        "Project",
				Kind:        tc.kind,
				Horizon:     tc.horizon,
				Status:      ProjectStatusActive,
			}

			if err := ValidateProject(project); err != nil {
				t.Fatalf("ValidateProject() error = %v", err)
			}
		})
	}
}

func TestProjectIdentityIsScopedByWorkspace(t *testing.T) {
	t.Parallel()

	first := Project{WorkspaceID: "workspace-a", ID: "personal"}
	second := Project{WorkspaceID: "workspace-b", ID: "personal"}

	if first.Identity() == second.Identity() {
		t.Fatal("projects with the same local id in different workspaces must have different identities")
	}
}

func TestValidateWorkspaceSystemProjectsRequiresExactlyOneInboxAndPersonal(t *testing.T) {
	t.Parallel()

	valid := []Project{
		{WorkspaceID: "workspace-a", ID: "system-inbox", SystemRole: ProjectSystemRoleInbox},
		{WorkspaceID: "workspace-a", ID: "personal", SystemRole: ProjectSystemRolePersonal},
	}
	if err := ValidateWorkspaceSystemProjects("workspace-a", valid); err != nil {
		t.Fatalf("valid system projects rejected: %v", err)
	}

	for _, tc := range []struct {
		name     string
		projects []Project
	}{
		{name: "missing inbox", projects: valid[1:]},
		{name: "missing personal", projects: valid[:1]},
		{name: "duplicate inbox", projects: append(append([]Project{}, valid...), Project{WorkspaceID: "workspace-a", ID: "inbox-2", SystemRole: ProjectSystemRoleInbox})},
		{name: "duplicate personal", projects: append(append([]Project{}, valid...), Project{WorkspaceID: "workspace-a", ID: "personal-2", SystemRole: ProjectSystemRolePersonal})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWorkspaceSystemProjects("workspace-a", tc.projects); !errors.Is(err, ErrInvalidSystemProjectSet) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidSystemProjectSet)
			}
		})
	}
}

func TestSystemProjectCannotBeDeletedOrChangeRole(t *testing.T) {
	t.Parallel()

	project := Project{WorkspaceID: "workspace-a", ID: "personal", SystemRole: ProjectSystemRolePersonal}
	if err := ValidateProjectDeletion(project); !errors.Is(err, ErrSystemProjectImmutable) {
		t.Fatalf("ValidateProjectDeletion() error = %v, want %v", err, ErrSystemProjectImmutable)
	}
	if _, err := ChangeProjectSystemRole(project, ProjectSystemRoleInbox); !errors.Is(err, ErrSystemProjectImmutable) {
		t.Fatalf("ChangeProjectSystemRole() error = %v, want %v", err, ErrSystemProjectImmutable)
	}

	renamed, err := RenameProject(project, "私人事务")
	if err != nil {
		t.Fatalf("RenameProject() error = %v", err)
	}
	if renamed.Name != "私人事务" || renamed.SystemRole != ProjectSystemRolePersonal {
		t.Fatalf("RenameProject() = %#v", renamed)
	}
}

func TestCompleteProjectRejectsNonTerminalOccurrences(t *testing.T) {
	t.Parallel()

	project := Project{WorkspaceID: "workspace-a", ID: "project-1", Status: ProjectStatusActive}
	if _, err := CompleteProject(project, 1); !errors.Is(err, ErrProjectHasOpenOccurrences) {
		t.Fatalf("CompleteProject() error = %v, want %v", err, ErrProjectHasOpenOccurrences)
	}

	completed, err := CompleteProject(project, 0)
	if err != nil {
		t.Fatalf("CompleteProject() error = %v", err)
	}
	if completed.Status != ProjectStatusCompleted {
		t.Fatalf("status = %q, want %q", completed.Status, ProjectStatusCompleted)
	}
}

func TestLearningProjectAllowsAtMostOneCurrentRoadmap(t *testing.T) {
	t.Parallel()

	project := Project{WorkspaceID: "workspace-a", ID: "learning-1", Kind: ProjectKindLearning}
	if err := ValidateProjectRoadmaps(project, []Roadmap{
		{WorkspaceID: "workspace-a", ID: "roadmap-1", ProjectID: project.ID, Current: true},
		{WorkspaceID: "workspace-a", ID: "roadmap-2", ProjectID: project.ID, Current: true},
	}); !errors.Is(err, ErrMultipleCurrentRoadmaps) {
		t.Fatalf("ValidateProjectRoadmaps() error = %v, want %v", err, ErrMultipleCurrentRoadmaps)
	}

	if err := ValidateProjectRoadmaps(project, []Roadmap{
		{WorkspaceID: "workspace-a", ID: "roadmap-1", ProjectID: project.ID, Current: false},
		{WorkspaceID: "workspace-a", ID: "roadmap-2", ProjectID: project.ID, Current: true},
	}); err != nil {
		t.Fatalf("one current roadmap rejected: %v", err)
	}
}
