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
	IsDefault  bool   `json:"is_default"`
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

type NoteSyncBinding struct {
	NoteID    string `json:"note_id"`
	TargetID  string `json:"target_id"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type SyncExternalClaim struct {
	ExternalKey  string `json:"external_key"`
	NoteID       string `json:"note_id"`
	TargetID     string `json:"target_id"`
	ExternalType string `json:"external_type"`
	ExternalID   string `json:"external_id"`
	ExternalPath string `json:"external_path"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type NoteSyncSuppression struct {
	NoteID    string `json:"note_id"`
	TargetID  string `json:"target_id"`
	Reason    string `json:"reason"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type SyncImportTombstone struct {
	ExternalKey  string `json:"external_key"`
	TargetID     string `json:"target_id"`
	FormerNoteID string `json:"former_note_id"`
	ExternalType string `json:"external_type"`
	ExternalID   string `json:"external_id"`
	ExternalPath string `json:"external_path"`
	Reason       string `json:"reason"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type SaveSyncTargetRequest struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name" binding:"required"`
	VaultPath  string `json:"vault_path"`
	BaseFolder string `json:"base_folder"`
	ConfigJSON string `json:"config_json"`
	Enabled    bool   `json:"enabled"`
	AutoSync   bool   `json:"auto_sync"`
	IsDefault  *bool  `json:"is_default"`
}

type SaveNoteSyncBindingRequest struct {
	TargetID             string `json:"target_id" binding:"required"`
	ExpectedTargetID     string `json:"expected_target_id"`
	ConfirmChangedTarget bool   `json:"confirm_changed_target"`
}

type DeleteNoteSyncBindingRequest struct {
	ExpectedTargetID  string `json:"expected_target_id" binding:"required"`
	ExpectedUpdatedAt int64  `json:"expected_updated_at" binding:"required"`
}

type NoteSyncBindingCandidate struct {
	Target SyncTarget `json:"target"`
	State  *SyncState `json:"state,omitempty"`
}

type NoteSyncBindingResponse struct {
	Binding    *NoteSyncBinding           `json:"binding"`
	Target     *SyncTarget                `json:"target,omitempty"`
	State      *SyncState                 `json:"state,omitempty"`
	Candidates []NoteSyncBindingCandidate `json:"candidates,omitempty"`
}

type SaveNoteSyncBindingResponse struct {
	Binding       NoteSyncBinding `json:"binding"`
	Target        SyncTarget      `json:"target"`
	ChangedTarget bool            `json:"changed_target"`
}

type TargetScopedSyncRequest struct {
	ConfirmConflicts bool `json:"confirm_conflicts"`
}

type TargetScopedDeletionRequest struct {
	ExpectedTargetID string `json:"expected_target_id,omitempty"`
}

type TargetScopedDeletionResponse struct {
	Items []ExternalDeletedNote `json:"items"`
}

type SyncCompatibilityFlags struct {
	BindingMismatch      bool   `json:"binding_mismatch"`
	DefaultTargetMissing bool   `json:"default_target_missing"`
	BindingRequired      bool   `json:"binding_required"`
	BoundTargetID        string `json:"bound_target_id,omitempty"`
	BoundTargetName      string `json:"bound_target_name,omitempty"`
}

type NoteSyncStateCompatibilityRequest struct {
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
}

type NoteSyncStateCompatibilityResponse struct {
	State  *SyncState             `json:"state"`
	Target *SyncTarget            `json:"target"`
	Flags  SyncCompatibilityFlags `json:"flags"`
}

type SyncResultItem struct {
	NoteID       string `json:"note_id"`
	Status       string `json:"status"`
	ExternalPath string `json:"external_path,omitempty"`
	ExternalID   string `json:"external_id,omitempty"`
	ExternalURL  string `json:"external_url,omitempty"`
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

type NotionBidirectionalResult struct {
	Pushed          int              `json:"pushed"`
	Pulled          int              `json:"pulled"`
	ConflictPulled  int              `json:"conflict_pulled"`
	Imported        int              `json:"imported"`
	ExternalDeleted int              `json:"external_deleted"`
	Unsupported     int              `json:"unsupported"`
	Failed          int              `json:"failed"`
	Items           []SyncResultItem `json:"items"`
}

type ExternalDeletedNote struct {
	NoteID       string `json:"note_id"`
	Title        string `json:"title"`
	ExternalPath string `json:"external_path"`
	LastSyncedAt *int64 `json:"last_synced_at"`
}
