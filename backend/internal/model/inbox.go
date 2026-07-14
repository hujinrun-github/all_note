package model

type InboxItem struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Body        *string `json:"body"`
	Source      string  `json:"source"`
	Archived    int     `json:"archived"`
	ConvertedTo *string `json:"converted_to"`
	CreatedAt   int64   `json:"created_at"`
	UpdatedAt   int64   `json:"updated_at"`
}

type CreateInboxRequest struct {
	Kind  string  `json:"kind" binding:"required"`
	Title string  `json:"title" binding:"required"`
	Body  *string `json:"body"`
}

type ConvertInboxRequest struct {
	Kind      string  `json:"kind" binding:"required"`
	Title     *string `json:"title"`
	Content   *string `json:"content"`
	ProjectID *string `json:"project_id"`
	Due       *int64  `json:"due"`
	Priority  *int    `json:"priority"`
}

type BatchInboxRequest struct {
	IDs    []string `json:"ids" binding:"required"`
	Action string   `json:"action" binding:"required"`
}
