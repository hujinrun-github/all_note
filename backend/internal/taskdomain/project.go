package taskdomain

import "strings"

func ValidateProject(project Project) error {
	if strings.TrimSpace(project.WorkspaceID) == "" ||
		strings.TrimSpace(project.ID) == "" ||
		strings.TrimSpace(project.Name) == "" ||
		!validProjectKind(project.Kind) ||
		!validProjectHorizon(project.Horizon) ||
		!validProjectStatus(project.Status) ||
		!validProjectSystemRole(project.SystemRole) {
		return ErrInvalidProject
	}
	return nil
}

func ValidateWorkspaceSystemProjects(workspaceID string, projects []Project) error {
	if strings.TrimSpace(workspaceID) == "" {
		return ErrInvalidSystemProjectSet
	}

	inboxCount := 0
	personalCount := 0
	for _, project := range projects {
		if project.WorkspaceID != workspaceID {
			continue
		}
		switch project.SystemRole {
		case ProjectSystemRoleInbox:
			inboxCount++
		case ProjectSystemRolePersonal:
			personalCount++
		}
	}
	if inboxCount != 1 || personalCount != 1 {
		return ErrInvalidSystemProjectSet
	}
	return nil
}

func ValidateProjectDeletion(project Project) error {
	if project.SystemRole != ProjectSystemRoleNone {
		return ErrSystemProjectImmutable
	}
	return nil
}

func ChangeProjectSystemRole(project Project, next ProjectSystemRole) (Project, error) {
	if project.SystemRole == next {
		return project, nil
	}
	if project.SystemRole != ProjectSystemRoleNone || next != ProjectSystemRoleNone {
		return project, ErrSystemProjectImmutable
	}
	project.SystemRole = next
	return project, nil
}

func RenameProject(project Project, name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return project, ErrInvalidProject
	}
	project.Name = name
	return project, nil
}

func CompleteProject(project Project, nonTerminalOccurrences int) (Project, error) {
	if nonTerminalOccurrences < 0 {
		return project, ErrInvalidProject
	}
	if nonTerminalOccurrences != 0 {
		return project, ErrProjectHasOpenOccurrences
	}
	project.Status = ProjectStatusCompleted
	return project, nil
}

func ValidateProjectRoadmaps(project Project, roadmaps []Roadmap) error {
	currentCount := 0
	for _, roadmap := range roadmaps {
		if roadmap.WorkspaceID != project.WorkspaceID || roadmap.ProjectID != project.ID {
			return ErrInvalidProject
		}
		if roadmap.Current {
			currentCount++
		}
	}
	if project.Kind == ProjectKindLearning && currentCount > 1 {
		return ErrMultipleCurrentRoadmaps
	}
	return nil
}

func validProjectKind(kind ProjectKind) bool {
	return kind == ProjectKindStandard || kind == ProjectKindLearning
}

func validProjectHorizon(horizon ProjectHorizon) bool {
	return horizon == ProjectHorizonShort || horizon == ProjectHorizonLong
}

func validProjectStatus(status ProjectStatus) bool {
	switch status {
	case ProjectStatusPlanning, ProjectStatusActive, ProjectStatusPaused, ProjectStatusCompleted, ProjectStatusArchived:
		return true
	default:
		return false
	}
}

func validProjectSystemRole(role ProjectSystemRole) bool {
	switch role {
	case ProjectSystemRoleNone, ProjectSystemRoleInbox, ProjectSystemRolePersonal:
		return true
	default:
		return false
	}
}
