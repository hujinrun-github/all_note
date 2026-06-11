package model

type LocalDirectoryEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	ModifiedAt int64  `json:"modified_at"`
}

type LocalDirectoryList struct {
	CurrentPath string                `json:"current_path"`
	ParentPath  string                `json:"parent_path,omitempty"`
	Entries     []LocalDirectoryEntry `json:"entries"`
}
