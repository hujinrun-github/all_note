package model

// NoteProject is a lightweight project reference included in note responses.
type NoteProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Note struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	FolderID  string `json:"folder_id"`
	Tags      string        `json:"tags"`
	Projects  []NoteProject `json:"projects"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type CreateNoteRequest struct {
	Title      string   `json:"title" binding:"required"`
	Body       string   `json:"body"`
	FolderID   string   `json:"folder_id"`
	Tags       string   `json:"tags"`
	ProjectIDs []string `json:"project_ids"`
}

type CreateNoteWithIDRequest struct {
	ID       string
	Title    string
	Body     string
	FolderID string
	Tags     string
}

type UpdateNoteRequest struct {
	Title      *string   `json:"title"`
	Body       *string   `json:"body"`
	FolderID   *string   `json:"folder_id"`
	Tags       *string   `json:"tags"`
	ProjectIDs *[]string `json:"project_ids"`
}
