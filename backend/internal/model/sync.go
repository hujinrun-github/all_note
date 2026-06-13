package model

type SyncTarget struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	VaultPath  string `json:"vault_path"`
	BaseFolder string `json:"base_folder"`
	ConfigJSON string `json:"config_json"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type SyncState struct {
	NoteID        string  `json:"note_id"`
	TargetID      string  `json:"target_id"`
	ExternalPath  string  `json:"external_path"`
	ExternalID    string  `json:"external_id"`
	ExternalURL   string  `json:"external_url"`
	ContentHash   string  `json:"content_hash"`
	ExternalHash  string  `json:"external_hash"`
	ExternalMTime *int64  `json:"external_mtime"`
	LastDirection string  `json:"last_direction"`
	LastSyncedAt  *int64  `json:"last_synced_at"`
	Status        string  `json:"status"`
	ErrorMessage  *string `json:"error_message"`
}

type SaveSyncTargetRequest struct {
	Name       string `json:"name" binding:"required"`
	VaultPath  string `json:"vault_path" binding:"required"`
	BaseFolder string `json:"base_folder" binding:"required"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
}

type SyncResultItem struct {
	NoteID       string `json:"note_id"`
	Status       string `json:"status"`
	ExternalPath string `json:"external_path,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type SyncBatchResult struct {
	Synced int              `json:"synced"`
	Failed int              `json:"failed"`
	Items  []SyncResultItem `json:"items"`
}

type ObsidianBidirectionalResult struct {
	Pushed          int              `json:"pushed"`
	Pulled          int              `json:"pulled"`
	Imported        int              `json:"imported"`
	ExternalDeleted int              `json:"external_deleted"`
	Failed          int              `json:"failed"`
	Items           []SyncResultItem `json:"items"`
}

type ExternalDeletedNote struct {
	NoteID       string `json:"note_id"`
	Title        string `json:"title"`
	ExternalPath string `json:"external_path"`
	LastSyncedAt *int64 `json:"last_synced_at"`
}
