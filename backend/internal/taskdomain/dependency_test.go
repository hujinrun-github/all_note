package taskdomain

import (
	"errors"
	"testing"
)

func TestAddTaskDependencyRejectsInvalidEdges(t *testing.T) {
	tasks := []DependencyTask{
		{WorkspaceID: "workspace-a", ID: "task-a", ProjectID: "project-a"},
		{WorkspaceID: "workspace-a", ID: "task-b", ProjectID: "project-a"},
		{WorkspaceID: "workspace-b", ID: "task-c", ProjectID: "project-b"},
	}
	existing := []TaskDependency{{
		WorkspaceID:       "workspace-a",
		PredecessorTaskID: "task-a",
		SuccessorTaskID:   "task-b",
		Type:              DependencyFinishToStart,
	}}

	tests := []struct {
		name      string
		candidate TaskDependency
	}{
		{
			name: "self edge",
			candidate: TaskDependency{
				WorkspaceID:       "workspace-a",
				PredecessorTaskID: "task-a",
				SuccessorTaskID:   "task-a",
				Type:              DependencyFinishToStart,
			},
		},
		{
			name:      "duplicate edge",
			candidate: existing[0],
		},
		{
			name: "cross workspace",
			candidate: TaskDependency{
				WorkspaceID:       "workspace-a",
				PredecessorTaskID: "task-a",
				SuccessorTaskID:   "task-c",
				Type:              DependencyFinishToStart,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AddTaskDependency(tasks, existing, test.candidate)
			if !errors.Is(err, ErrInvalidTaskDependency) {
				t.Fatalf("AddTaskDependency() error = %v, want %v", err, ErrInvalidTaskDependency)
			}
			if ErrorCodeOf(err) != ErrorCodeInvalidTaskDependency {
				t.Fatalf("error code = %q, want %q", ErrorCodeOf(err), ErrorCodeInvalidTaskDependency)
			}
		})
	}
}

func TestFinishToStartRejectsRecurringEndpoint(t *testing.T) {
	tests := []struct {
		name  string
		tasks []DependencyTask
	}{
		{
			name: "recurring predecessor",
			tasks: []DependencyTask{
				{WorkspaceID: "workspace-a", ID: "task-a", ProjectID: "project-a", Recurring: true},
				{WorkspaceID: "workspace-a", ID: "task-b", ProjectID: "project-a"},
			},
		},
		{
			name: "recurring successor",
			tasks: []DependencyTask{
				{WorkspaceID: "workspace-a", ID: "task-a", ProjectID: "project-a"},
				{WorkspaceID: "workspace-a", ID: "task-b", ProjectID: "project-a", Recurring: true},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AddTaskDependency(test.tasks, nil, TaskDependency{
				WorkspaceID:       "workspace-a",
				PredecessorTaskID: "task-a",
				SuccessorTaskID:   "task-b",
				Type:              DependencyFinishToStart,
			})
			if !errors.Is(err, ErrRecurringTaskDependency) {
				t.Fatalf("AddTaskDependency() error = %v, want %v", err, ErrRecurringTaskDependency)
			}
		})
	}
}

func TestFinishToStartRejectsDirectedCycle(t *testing.T) {
	tasks := []DependencyTask{
		{WorkspaceID: "workspace-a", ID: "task-a"},
		{WorkspaceID: "workspace-a", ID: "task-b"},
		{WorkspaceID: "workspace-a", ID: "task-c"},
	}
	existing := []TaskDependency{
		{WorkspaceID: "workspace-a", PredecessorTaskID: "task-a", SuccessorTaskID: "task-b", Type: DependencyFinishToStart},
		{WorkspaceID: "workspace-a", PredecessorTaskID: "task-b", SuccessorTaskID: "task-c", Type: DependencyFinishToStart},
	}

	_, err := AddTaskDependency(tasks, existing, TaskDependency{
		WorkspaceID:       "workspace-a",
		PredecessorTaskID: "task-c",
		SuccessorTaskID:   "task-a",
		Type:              DependencyFinishToStart,
	})
	if !errors.Is(err, ErrTaskDependencyCycle) {
		t.Fatalf("AddTaskDependency() error = %v, want %v", err, ErrTaskDependencyCycle)
	}

	if _, err := AddTaskDependency(tasks, existing, TaskDependency{
		WorkspaceID:       "workspace-a",
		PredecessorTaskID: "task-c",
		SuccessorTaskID:   "task-a",
		Type:              DependencyRelated,
	}); err != nil {
		t.Fatalf("related edge must not participate in blocking cycle detection: %v", err)
	}
}

func TestIsTaskBlockedOnlyUsesFinishToStart(t *testing.T) {
	task := DependencyTask{WorkspaceID: "workspace-a", ID: "task-b"}
	dependencies := []TaskDependency{
		{WorkspaceID: "workspace-a", PredecessorTaskID: "blocking", SuccessorTaskID: "task-b", Type: DependencyFinishToStart},
		{WorkspaceID: "workspace-a", PredecessorTaskID: "related", SuccessorTaskID: "task-b", Type: DependencyRelated},
		{WorkspaceID: "workspace-a", PredecessorTaskID: "suggested", SuccessorTaskID: "task-b", Type: DependencySuggestedOrder},
	}

	if !IsTaskBlocked(task, dependencies, map[string]bool{}) {
		t.Fatal("incomplete finish_to_start predecessor must block task")
	}
	if IsTaskBlocked(task, dependencies, map[string]bool{"blocking": true}) {
		t.Fatal("related and suggested_order predecessors must not block task")
	}
}

func TestValidateRoadmapTaskLinkRequiresSameLearningProject(t *testing.T) {
	validProject := Project{WorkspaceID: "workspace-a", ID: "learning-a", Kind: ProjectKindLearning}
	validTask := DependencyTask{WorkspaceID: "workspace-a", ID: "task-a", ProjectID: "learning-a"}
	validNode := RoadmapNode{WorkspaceID: "workspace-a", ID: "node-a", ProjectID: "learning-a"}
	if err := ValidateRoadmapTaskLink(validProject, validTask, validNode); err != nil {
		t.Fatalf("ValidateRoadmapTaskLink() unexpected error: %v", err)
	}

	tests := []struct {
		name    string
		project Project
		task    DependencyTask
		node    RoadmapNode
	}{
		{name: "standard project", project: Project{WorkspaceID: "workspace-a", ID: "learning-a", Kind: ProjectKindStandard}, task: validTask, node: validNode},
		{name: "task in another project", project: validProject, task: DependencyTask{WorkspaceID: "workspace-a", ID: "task-a", ProjectID: "learning-b"}, node: validNode},
		{name: "node in another project", project: validProject, task: validTask, node: RoadmapNode{WorkspaceID: "workspace-a", ID: "node-a", ProjectID: "learning-b"}},
		{name: "task in another workspace", project: validProject, task: DependencyTask{WorkspaceID: "workspace-b", ID: "task-a", ProjectID: "learning-a"}, node: validNode},
		{name: "node in another workspace", project: validProject, task: validTask, node: RoadmapNode{WorkspaceID: "workspace-b", ID: "node-a", ProjectID: "learning-a"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRoadmapTaskLink(test.project, test.task, test.node)
			if !errors.Is(err, ErrInvalidRoadmapTaskLink) {
				t.Fatalf("ValidateRoadmapTaskLink() error = %v, want %v", err, ErrInvalidRoadmapTaskLink)
			}
			if ErrorCodeOf(err) != ErrorCodeInvalidRoadmapTaskLink {
				t.Fatalf("error code = %q, want %q", ErrorCodeOf(err), ErrorCodeInvalidRoadmapTaskLink)
			}
		})
	}
}
