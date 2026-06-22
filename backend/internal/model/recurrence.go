package model

type RecurrenceConfig struct {
	StartDate string  `json:"start_date" binding:"required"`
	EndDate   *string `json:"end_date"`
	Frequency string  `json:"frequency" binding:"required"`
	Interval  int     `json:"interval"`
	Weekdays  []int   `json:"weekdays"`
	MonthDays []int   `json:"month_days"`
	Timezone  string  `json:"timezone"`
	Enabled   *bool   `json:"enabled"`
}

type RecurrenceRule struct {
	TaskID    string  `json:"task_id"`
	StartDate string  `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Frequency string  `json:"frequency"`
	Interval  int     `json:"interval"`
	Weekdays  []int   `json:"weekdays"`
	MonthDays []int   `json:"month_days"`
	Timezone  string  `json:"timezone"`
	Enabled   bool    `json:"enabled"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type TaskOccurrence struct {
	TaskID          string  `json:"task_id"`
	OccurrenceDate  string  `json:"occurrence_date"`
	Status          string  `json:"status"`
	CompletedAt     *int64  `json:"completed_at,omitempty"`
	Note            string  `json:"note"`
	Title           string  `json:"title,omitempty"`
	Content         string  `json:"content,omitempty"`
	ProjectID       *string `json:"project_id,omitempty"`
	Project         string  `json:"project,omitempty"`
	RoadmapNodeID   *string `json:"roadmap_node_id,omitempty"`
	RecurrenceLabel string  `json:"recurrence_label,omitempty"`
	SortOrder       float64 `json:"sort_order"`
	CreatedAt       int64   `json:"created_at"`
}
