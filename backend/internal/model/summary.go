package model

type SummaryData struct {
	Groups       []DateGroup `json:"groups"`
	ActiveDays   int         `json:"active_days"`
	ProjectCount int         `json:"project_count"`
	total        int
}

func NewSummaryData(groups []DateGroup, activeDays, projectCount, total int) *SummaryData {
	return &SummaryData{Groups: groups, ActiveDays: activeDays, ProjectCount: projectCount, total: total}
}

func (s *SummaryData) PaginationTotal() int { return s.total }

type DateGroup struct {
	Date  string        `json:"date"`
	Tasks []TaskSummary `json:"tasks"`
	Count int           `json:"count"`
}

type TaskSummary struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Done        int          `json:"done"`
	PlannedDate *string      `json:"planned_date,omitempty"`
	Due         *int64       `json:"due,omitempty"`
	CompletedAt *int64       `json:"completed_at,omitempty"`
	NoteID      *string      `json:"note_id,omitempty"`
	Project     *TaskProject `json:"project,omitempty"`
	LinkedNotes   []NoteRef `json:"linked_notes,omitempty"`
	ExecutionType  string   `json:"execution_type,omitempty"`
	OccurrenceDate string   `json:"occurrence_date,omitempty"`
}

type NoteRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type SummaryParams struct {
	From, To int64
	Page, PageSize int
}
