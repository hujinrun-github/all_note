package model

type Folder struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	SortOrder float64 `json:"sort_order"`
	NoteCount int     `json:"note_count"`
	CreatedAt int64   `json:"created_at"`
}
