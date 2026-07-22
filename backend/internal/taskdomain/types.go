package taskdomain

// ProjectKind describes the capabilities enabled for a project.
type ProjectKind string

const (
	ProjectKindStandard ProjectKind = "standard"
	ProjectKindLearning ProjectKind = "learning"
)

// ProjectHorizon is an independent planning dimension and does not imply a
// project kind.
type ProjectHorizon string

const (
	ProjectHorizonShort ProjectHorizon = "short"
	ProjectHorizonLong  ProjectHorizon = "long"
)

type ProjectStatus string

const (
	ProjectStatusPlanning  ProjectStatus = "planning"
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusPaused    ProjectStatus = "paused"
	ProjectStatusCompleted ProjectStatus = "completed"
	ProjectStatusArchived  ProjectStatus = "archived"
)

type ProjectSystemRole string

const (
	ProjectSystemRoleNone     ProjectSystemRole = ""
	ProjectSystemRoleInbox    ProjectSystemRole = "inbox"
	ProjectSystemRolePersonal ProjectSystemRole = "personal"
)

type ProjectIdentity struct {
	WorkspaceID string
	ProjectID   string
}

type Project struct {
	WorkspaceID string
	ID          string
	Name        string
	Kind        ProjectKind
	Horizon     ProjectHorizon
	Status      ProjectStatus
	SystemRole  ProjectSystemRole
}

func (p Project) Identity() ProjectIdentity {
	return ProjectIdentity{WorkspaceID: p.WorkspaceID, ProjectID: p.ID}
}

type Roadmap struct {
	WorkspaceID string
	ID          string
	ProjectID   string
	Current     bool
}

type TaskLifecycleStatus string

const (
	TaskLifecycleDraft     TaskLifecycleStatus = "draft"
	TaskLifecycleActive    TaskLifecycleStatus = "active"
	TaskLifecyclePaused    TaskLifecycleStatus = "paused"
	TaskLifecycleCompleted TaskLifecycleStatus = "completed"
	TaskLifecycleCancelled TaskLifecycleStatus = "cancelled"
	TaskLifecycleArchived  TaskLifecycleStatus = "archived"
)

// TaskDefinition contains stable task attributes. Per-execution state belongs
// to an occurrence rather than this definition.
type TaskDefinition struct {
	Title           string
	Description     string
	Priority        int
	LifecycleStatus TaskLifecycleStatus
}

// TaskPatch deliberately models lifecycle as a rejected field so callers
// cannot accidentally treat a state transition as an ordinary attribute edit.
type TaskPatch struct {
	Title           string
	Description     string
	Priority        int
	LifecycleStatus *TaskLifecycleStatus
}
