package model

type Note struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	FolderID  string `json:"folder_id"`
	Tags      string `json:"tags"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type CreateNoteRequest struct {
	Title    string `json:"title" binding:"required"`
	Body     string `json:"body"`
	FolderID string `json:"folder_id"`
	Tags     string `json:"tags"`
}

type UpdateNoteRequest struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	FolderID *string `json:"folder_id"`
	Tags     *string `json:"tags"`
}
