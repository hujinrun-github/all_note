package model

type Event struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	StartTime   int64   `json:"start_time"`
	EndTime     int64   `json:"end_time"`
	Location    *string `json:"location"`
	Kind        string  `json:"kind"`
	NoteID      *string `json:"note_id"`
	ProjectID   *string `json:"project_id"`
	Project     *string `json:"project,omitempty"`
	ProjectType *string `json:"project_type,omitempty"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

type CreateEventRequest struct {
	Title     string  `json:"title" binding:"required"`
	StartTime int64   `json:"start_time" binding:"required"`
	EndTime   int64   `json:"end_time" binding:"required"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
	ProjectID *string `json:"project_id"`
}

type UpdateEventRequest struct {
	Title     *string `json:"title"`
	StartTime *int64  `json:"start_time"`
	EndTime   *int64  `json:"end_time"`
	Location  *string `json:"location"`
	Kind      *string `json:"kind"`
	ProjectID *string `json:"project_id,omitempty"`
}
