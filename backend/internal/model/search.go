package model

type SearchResult struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Highlight string `json:"highlight"`
	// note-only fields
	FolderID *string `json:"folder_id,omitempty"`
	// task-only fields
	Done *int `json:"done,omitempty"`
	// event-only fields
	Kind *string `json:"kind,omitempty"`
	// common
	UpdatedAt int64 `json:"updated_at"`
}
