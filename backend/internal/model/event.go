package model

type Event struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	StartTime int64   `json:"start_time"`
	EndTime   int64   `json:"end_time"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
	NoteID    *string `json:"note_id"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type CreateEventRequest struct {
	Title     string  `json:"title" binding:"required"`
	StartTime int64   `json:"start_time" binding:"required"`
	EndTime   int64   `json:"end_time" binding:"required"`
	Location  *string `json:"location"`
	Kind      string  `json:"kind"`
}

type UpdateEventRequest struct {
	Title     *string `json:"title"`
	StartTime *int64  `json:"start_time"`
	EndTime   *int64  `json:"end_time"`
	Location  *string `json:"location"`
	Kind      *string `json:"kind"`
}
