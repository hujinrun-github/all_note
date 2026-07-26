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

func TestProjectLifecycleTransitions(t *testing.T) {
	t.Parallel()

	project := Project{WorkspaceID: "workspace-a", ID: "project-1", Status: ProjectStatusPlanning}

	active, err := ActivateProject(project)
	if err != nil || active.Status != ProjectStatusActive {
		t.Fatalf("ActivateProject() = %#v, %v", active, err)
	}
	paused, err := PauseProject(active)
	if err != nil || paused.Status != ProjectStatusPaused {
		t.Fatalf("PauseProject() = %#v, %v", paused, err)
	}
	resumed, err := ResumeProject(paused)
	if err != nil || resumed.Status != ProjectStatusActive {
		t.Fatalf("ResumeProject() = %#v, %v", resumed, err)
	}
	completed, err := CompleteProject(resumed, 0)
	if err != nil || completed.Status != ProjectStatusCompleted {
		t.Fatalf("CompleteProject() = %#v, %v", completed, err)
	}
}

func TestProjectLifecycleRejectsIllegalTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		project    Project
		transition func(Project) (Project, error)
	}{
		{name: "activate active", project: Project{Status: ProjectStatusActive}, transition: ActivateProject},
		{name: "pause planning", project: Project{Status: ProjectStatusPlanning}, transition: PauseProject},
		{name: "resume active", project: Project{Status: ProjectStatusActive}, transition: ResumeProject},
		{name: "complete planning", project: Project{Status: ProjectStatusPlanning}, transition: func(project Project) (Project, error) {
			return CompleteProject(project, 0)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.transition(test.project); !errors.Is(err, ErrInvalidProjectTransition) {
				t.Fatalf("transition error = %v, want %v", err, ErrInvalidProjectTransition)
			}
		})
	}
}

func TestArchiveAndRestoreProjectPreserveExactPreviousStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []ProjectStatus{
		ProjectStatusPlanning,
		ProjectStatusActive,
		ProjectStatusPaused,
		ProjectStatusCompleted,
	} {
		t.Run(string(status), func(t *testing.T) {
			project := Project{Status: status}
			archived, err := ArchiveProject(project)
			if err != nil {
				t.Fatalf("ArchiveProject() error = %v", err)
			}
			if archived.Status != ProjectStatusArchived || archived.ArchivedFromStatus == nil || *archived.ArchivedFromStatus != status {
				t.Fatalf("archived project = %#v", archived)
			}

			restored, err := RestoreProject(archived, nil)
			if err != nil {
				t.Fatalf("RestoreProject() error = %v", err)
			}
			if restored.Status != status || restored.ArchivedFromStatus != nil {
				t.Fatalf("restored project = %#v", restored)
			}
		})
	}
}

func TestRestoreLegacyArchivedProjectRequiresExplicitTarget(t *testing.T) {
	t.Parallel()

	legacy := Project{Status: ProjectStatusArchived}
	if _, err := RestoreProject(legacy, nil); !errors.Is(err, ErrRestoreTargetRequired) {
		t.Fatalf("RestoreProject() error = %v, want %v", err, ErrRestoreTargetRequired)
	}

	restoreTo := ProjectStatusPaused
	restored, err := RestoreProject(legacy, &restoreTo)
	if err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if restored.Status != ProjectStatusPaused || restored.ArchivedFromStatus != nil {
		t.Fatalf("restored project = %#v", restored)
	}

	invalid := ProjectStatusArchived
	if _, err := RestoreProject(legacy, &invalid); !errors.Is(err, ErrInvalidProjectTransition) {
		t.Fatalf("invalid restore error = %v, want %v", err, ErrInvalidProjectTransition)
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
