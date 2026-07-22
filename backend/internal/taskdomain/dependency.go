package taskdomain

import "strings"

type DependencyType string

const (
	DependencyFinishToStart  DependencyType = "finish_to_start"
	DependencyRelated        DependencyType = "related"
	DependencySuggestedOrder DependencyType = "suggested_order"
)

const (
	ErrorCodeInvalidTaskDependency   ErrorCode = "invalid_task_dependency"
	ErrorCodeRecurringTaskDependency ErrorCode = "recurring_task_dependency"
	ErrorCodeTaskDependencyCycle     ErrorCode = "task_dependency_cycle"
	ErrorCodeInvalidRoadmapTaskLink  ErrorCode = "invalid_roadmap_task_link"
)

var (
	ErrInvalidTaskDependency   = &domainError{code: ErrorCodeInvalidTaskDependency}
	ErrRecurringTaskDependency = &domainError{code: ErrorCodeRecurringTaskDependency}
	ErrTaskDependencyCycle     = &domainError{code: ErrorCodeTaskDependencyCycle}
	ErrInvalidRoadmapTaskLink  = &domainError{code: ErrorCodeInvalidRoadmapTaskLink}
)

// DependencyTask is the stable task identity and the scheduling property that
// matters when validating task-level dependencies.
type DependencyTask struct {
	WorkspaceID string
	ID          string
	ProjectID   string
	Recurring   bool
}

type TaskDependency struct {
	WorkspaceID       string
	PredecessorTaskID string
	SuccessorTaskID   string
	Type              DependencyType
}

type RoadmapNode struct {
	WorkspaceID string
	ID          string
	ProjectID   string
	RoadmapID   string
	ParentID    string
	Title       string
	Description string
	Type        RoadmapNodeType
	Position    float64
	Revision    int64
}

// AddTaskDependency validates a candidate against the current workspace graph
// and returns a new slice. The input graph remains unchanged on success and
// failure, which keeps validation safe to use before persistence.
func AddTaskDependency(tasks []DependencyTask, existing []TaskDependency, candidate TaskDependency) ([]TaskDependency, error) {
	if !validTaskDependency(candidate) || candidate.PredecessorTaskID == candidate.SuccessorTaskID {
		return existing, ErrInvalidTaskDependency
	}

	predecessor, predecessorOK := findDependencyTask(tasks, candidate.WorkspaceID, candidate.PredecessorTaskID)
	successor, successorOK := findDependencyTask(tasks, candidate.WorkspaceID, candidate.SuccessorTaskID)
	if !predecessorOK || !successorOK {
		return existing, ErrInvalidTaskDependency
	}

	for _, edge := range existing {
		if edge.WorkspaceID == candidate.WorkspaceID &&
			edge.PredecessorTaskID == candidate.PredecessorTaskID &&
			edge.SuccessorTaskID == candidate.SuccessorTaskID &&
			edge.Type == candidate.Type {
			return existing, ErrInvalidTaskDependency
		}
	}

	if candidate.Type == DependencyFinishToStart {
		if predecessor.Recurring || successor.Recurring {
			return existing, ErrRecurringTaskDependency
		}
		if createsFinishToStartCycle(existing, candidate) {
			return existing, ErrTaskDependencyCycle
		}
	}

	result := make([]TaskDependency, len(existing), len(existing)+1)
	copy(result, existing)
	return append(result, candidate), nil
}

// IsTaskBlocked reports execution blocking only. Structural associations and
// recommended ordering deliberately do not affect execution.
func IsTaskBlocked(task DependencyTask, dependencies []TaskDependency, completedTaskIDs map[string]bool) bool {
	for _, edge := range dependencies {
		if edge.WorkspaceID == task.WorkspaceID &&
			edge.SuccessorTaskID == task.ID &&
			edge.Type == DependencyFinishToStart &&
			!completedTaskIDs[edge.PredecessorTaskID] {
			return true
		}
	}
	return false
}

func ValidateRoadmapTaskLink(project Project, task DependencyTask, node RoadmapNode) error {
	if project.Kind != ProjectKindLearning ||
		strings.TrimSpace(project.WorkspaceID) == "" ||
		strings.TrimSpace(project.ID) == "" ||
		strings.TrimSpace(task.ID) == "" ||
		strings.TrimSpace(node.ID) == "" ||
		task.WorkspaceID != project.WorkspaceID ||
		node.WorkspaceID != project.WorkspaceID ||
		task.ProjectID != project.ID ||
		node.ProjectID != project.ID {
		return ErrInvalidRoadmapTaskLink
	}
	return nil
}

func validTaskDependency(edge TaskDependency) bool {
	if strings.TrimSpace(edge.WorkspaceID) == "" ||
		strings.TrimSpace(edge.PredecessorTaskID) == "" ||
		strings.TrimSpace(edge.SuccessorTaskID) == "" {
		return false
	}
	switch edge.Type {
	case DependencyFinishToStart, DependencyRelated, DependencySuggestedOrder:
		return true
	default:
		return false
	}
}

func findDependencyTask(tasks []DependencyTask, workspaceID, taskID string) (DependencyTask, bool) {
	for _, task := range tasks {
		if task.WorkspaceID == workspaceID && task.ID == taskID {
			return task, true
		}
	}
	return DependencyTask{}, false
}

func createsFinishToStartCycle(existing []TaskDependency, candidate TaskDependency) bool {
	adjacency := make(map[string][]string)
	for _, edge := range existing {
		if edge.WorkspaceID == candidate.WorkspaceID && edge.Type == DependencyFinishToStart {
			adjacency[edge.PredecessorTaskID] = append(adjacency[edge.PredecessorTaskID], edge.SuccessorTaskID)
		}
	}

	visited := make(map[string]bool)
	var reachesPredecessor func(string) bool
	reachesPredecessor = func(taskID string) bool {
		if taskID == candidate.PredecessorTaskID {
			return true
		}
		if visited[taskID] {
			return false
		}
		visited[taskID] = true
		for _, next := range adjacency[taskID] {
			if reachesPredecessor(next) {
				return true
			}
		}
		return false
	}

	return reachesPredecessor(candidate.SuccessorTaskID)
}
