package model

type Task struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	Project       *string `json:"project"`
	ProjectID     *string `json:"project_id"`
	ProjectType   *string `json:"project_type,omitempty"`
	Due           *int64  `json:"due"`
	PlannedDate   *string `json:"planned_date"`
	Priority      int     `json:"priority"`
	Done          int     `json:"done"`
	Status        string  `json:"status"`
	Horizon       string  `json:"horizon"`
	Scope         string  `json:"scope"`
	SortOrder     float64 `json:"sort_order"`
	NoteID        *string `json:"note_id"`
	RoadmapNodeID *string `json:"roadmap_node_id"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
	CompletedAt     *int64  `json:"completed_at,omitempty"`
	ExecutionType    string  `json:"execution_type,omitempty"`
	OccurrenceDate   *string `json:"occurrence_date,omitempty"`
	OccurrenceStatus *string `json:"occurrence_status,omitempty"`
	RecurrenceLabel  *string `json:"recurrence_label,omitempty"`
}

type CreateTaskRequest struct {
	Title         string  `json:"title" binding:"required"`
	Content       string  `json:"content"`
	Project       *string `json:"project"`
	ProjectID     *string `json:"project_id"`
	Due           *int64  `json:"due"`
	PlannedDate   *string `json:"planned_date"`
	Priority      int     `json:"priority"`
	Scope         string  `json:"scope"`
	Horizon       string  `json:"horizon"`
	RoadmapNodeID *string            `json:"roadmap_node_id"`
	ExecutionType string             `json:"execution_type"`
	Recurrence    *RecurrenceConfig  `json:"recurrence"`
}

type UpdateTaskRequest struct {
	Title         *string  `json:"title"`
	Content       *string  `json:"content"`
	Project       *string  `json:"project"`
	ProjectID     *string  `json:"project_id"`
	Due           *int64   `json:"due"`
	PlannedDate   *string  `json:"planned_date"`
	Priority      *int     `json:"priority"`
	Done          *int     `json:"done"`
	Status        *string  `json:"status"`
	Scope         *string  `json:"scope"`
	Horizon       *string  `json:"horizon"`
	SortOrder      *float64          `json:"sort_order"`
	RoadmapNodeID  *string           `json:"roadmap_node_id"`
	ExecutionType  *string           `json:"execution_type"`
	Recurrence     *RecurrenceConfig `json:"recurrence"`
	Enabled        *bool             `json:"enabled"`
	EndDate        *string           `json:"end_date"`
}

type TaskProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type CreateTaskProjectRequest struct {
	Name        string `json:"name" binding:"required"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type UpdateTaskProjectRequest struct {
	Name        *string `json:"name"`
	Type        *string `json:"type"`
	Description *string `json:"description"`
}

type LearningRoadmap struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"project_id"`
	Title     string        `json:"title"`
	Goal      string        `json:"goal"`
	Status    string        `json:"status"`
	Nodes     []RoadmapNode `json:"nodes"`
	Edges     []RoadmapEdge `json:"edges"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64         `json:"updated_at"`
}

type RoadmapNode struct {
	ID                   string            `json:"id"`
	RoadmapID            string            `json:"roadmap_id"`
	ParentID             *string           `json:"parent_id"`
	Type                 string            `json:"type"`
	Title                string            `json:"title"`
	Description          string            `json:"description"`
	PathType             string            `json:"path_type"`
	Status               string            `json:"status"`
	Deliverable          string            `json:"deliverable"`
	AcceptanceCriteria   string            `json:"acceptance_criteria"`
	X                    float64           `json:"x"`
	Y                    float64           `json:"y"`
	OrderIndex           int               `json:"order_index"`
	ArticleSearchQueries []string          `json:"article_search_queries,omitempty"`
	Resources            []RoadmapResource `json:"resources"`
	CreatedAt            int64             `json:"created_at"`
	UpdatedAt            int64             `json:"updated_at"`
}

type RoadmapEdge struct {
	ID           string `json:"id"`
	RoadmapID    string `json:"roadmap_id"`
	SourceNodeID string `json:"source_node_id"`
	TargetNodeID string `json:"target_node_id"`
	Style        string `json:"style"`
	CreatedAt    int64  `json:"created_at"`
}

type RoadmapResource struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	Summary    string `json:"summary"`
	SourceType string `json:"source_type"`
	AddedBy    string `json:"added_by"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type CreateRoadmapResourceRequest struct {
	Title   string `json:"title" binding:"required"`
	URL     string `json:"url" binding:"required"`
	Summary string `json:"summary"`
}

type SearchRoadmapResourcesRequest struct {
	Sources []string `json:"sources"`
}

type CreateRoadmapNodeRequest struct {
	ParentID           *string  `json:"parent_id"`
	Title              string   `json:"title" binding:"required"`
	Type               string   `json:"type"`
	Description        string   `json:"description"`
	PathType           string   `json:"path_type"`
	Status             string   `json:"status"`
	Deliverable        string   `json:"deliverable"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	X                  *float64 `json:"x"`
	Y                  *float64 `json:"y"`
	EdgeStyle          string   `json:"edge_style"`
}

type UpdateRoadmapNodeRequest struct {
	Title              *string  `json:"title"`
	Description        *string  `json:"description"`
	PathType           *string  `json:"path_type"`
	Status             *string  `json:"status"`
	Deliverable        *string  `json:"deliverable"`
	AcceptanceCriteria *string  `json:"acceptance_criteria"`
	X                  *float64 `json:"x"`
	Y                  *float64 `json:"y"`
}

type RoadmapLayoutNode struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
}

type UpdateRoadmapLayoutRequest struct {
	Nodes []RoadmapLayoutNode `json:"nodes"`
}
