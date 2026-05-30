package model

type Task struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Project   *string `json:"project"`
	Due       *int64  `json:"due"`
	Priority  int     `json:"priority"`
	Done      int     `json:"done"`
	Scope     string  `json:"scope"`
	SortOrder float64 `json:"sort_order"`
	NoteID    *string `json:"note_id"`
	CreatedAt int64   `json:"created_at"`
	UpdatedAt int64   `json:"updated_at"`
}

type CreateTaskRequest struct {
	Title    string  `json:"title" binding:"required"`
	Project  *string `json:"project"`
	Due      *int64  `json:"due"`
	Priority int     `json:"priority"`
	Scope    string  `json:"scope"`
}

type UpdateTaskRequest struct {
	Title     *string  `json:"title"`
	Project   *string  `json:"project"`
	Due       *int64   `json:"due"`
	Priority  *int     `json:"priority"`
	Done      *int     `json:"done"`
	Scope     *string  `json:"scope"`
	SortOrder *float64 `json:"sort_order"`
}
