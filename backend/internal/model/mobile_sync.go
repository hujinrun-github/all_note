package model

import "encoding/json"

const (
	MobileOperationNoteCreate        = "note.create"
	MobileOperationNoteUpdate        = "note.update"
	MobileOperationNoteDelete        = "note.delete"
	MobileOperationNoteServerCreated = "note.server_created"
	MobileOperationNoteServerUpdated = "note.server_updated"
	MobileOperationNoteServerDeleted = "note.server_deleted"

	MobileMutationApplied  = "applied"
	MobileMutationConflict = "conflict"
)

type MobileNotePayload struct {
	Title    *string `json:"title,omitempty"`
	Body     *string `json:"body,omitempty"`
	FolderID *string `json:"folder_id,omitempty"`
	Tags     *string `json:"tags,omitempty"`
}

type MobileNoteMutation struct {
	MutationID     string            `json:"mutation_id"`
	DeviceClientID string            `json:"device_client_id"`
	EntityClientID string            `json:"entity_client_id"`
	Operation      string            `json:"operation"`
	BaseRevision   *int64            `json:"base_revision,omitempty"`
	RequestSHA256  string            `json:"request_sha256"`
	Payload        MobileNotePayload `json:"payload"`
}

type MobileEntityMutation struct {
	MutationID     string
	DeviceClientID string
	EntityType     string
	EntityClientID string
	Operation      string
	BaseRevision   *int64
	RequestSHA256  string
	Payload        json.RawMessage
}

type MobileEntityEnvelope struct {
	EntityType string          `json:"entity_type"`
	ID         string          `json:"id"`
	ClientID   string          `json:"client_id"`
	Revision   int64           `json:"revision"`
	DeletedAt  *int64          `json:"deleted_at,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

type MobileMutationResult struct {
	MutationID string                `json:"mutation_id"`
	Status     string                `json:"status"`
	ErrorCode  string                `json:"error_code,omitempty"`
	Entity     *MobileEntityEnvelope `json:"entity,omitempty"`
}

type MobileChange struct {
	Sequence   int64                `json:"sequence"`
	MutationID string               `json:"mutation_id"`
	Operation  string               `json:"operation"`
	Entity     MobileEntityEnvelope `json:"entity"`
}

type MobileCommittedChange struct {
	Position  int64                `json:"position"`
	Operation string               `json:"operation"`
	Entity    MobileEntityEnvelope `json:"entity"`
}

type MobileCommittedChangePage struct {
	Changes      []MobileCommittedChange `json:"changes"`
	NextPosition int64                   `json:"next_position"`
	HasMore      bool                    `json:"has_more"`
}

type BeginMobileSnapshot struct {
	SessionID string
	Scope     string
	TimeZone  string
	Now       int64
	ExpiresAt int64
}

type MobileSnapshot struct {
	SessionID        string
	Scope            string
	BoundaryPosition int64
	ExpiresAt        int64
	ScopeValidUntil  int64
	TimeZone         string
	TotalEntities    int64
}

type ReadMobileSnapshot struct {
	SessionID string
	Offset    int64
	Limit     int
	Now       int64
}

type MobileSnapshotPage struct {
	Entities         []MobileEntityEnvelope
	NextOffset       int64
	HasMore          bool
	BoundaryPosition int64
	ExpiresAt        int64
	ScopeValidUntil  int64
	TimeZone         string
}

type MobileSyncConflict struct {
	ConflictID     string          `json:"conflict_id"`
	EntityType     string          `json:"entity_type"`
	EntityClientID string          `json:"entity_id"`
	Operation      string          `json:"operation"`
	BaseRevision   int64           `json:"base_revision"`
	RemoteRevision int64           `json:"remote_revision"`
	LocalPayload   json.RawMessage `json:"local_payload"`
	RemotePayload  json.RawMessage `json:"remote_payload"`
	Revision       int64           `json:"revision"`
	ResolvedAt     *int64          `json:"resolved_at,omitempty"`
}

type CreateMobileSyncConflict struct {
	ConflictID     string
	MutationID     string
	DeviceClientID string
	RequestSHA256  string
	EntityType     string
	EntityClientID string
	Operation      string
	BaseRevision   int64
	LocalPayload   json.RawMessage
}

type ResolveMobileSyncConflict struct {
	ConflictID       string
	MutationID       string
	RequestSHA256    string
	ConflictRevision int64
	TargetRevision   int64
	Resolution       string
	MergedPayload    json.RawMessage
}
