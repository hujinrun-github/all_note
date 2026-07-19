package mobilesync

import (
	"encoding/json"
	"errors"
)

const (
	MaxBatchMutations = 100
	MaxBatchBytes     = 1024 * 1024
)

var (
	ErrBatchTooLarge = errors.New("mobile mutation batch exceeds limits")
	ErrInvalidBatch  = errors.New("mobile mutation batch is invalid")
)

type MutationBatch struct {
	ClientID  string          `json:"client_id"`
	Mutations []MutationInput `json:"mutations"`
}

type MutationInput struct {
	MutationID        string          `json:"mutation_id"`
	Operation         string          `json:"operation"`
	EntityID          string          `json:"entity_id"`
	BaseRevision      *int64          `json:"base_revision,omitempty"`
	DependsOnMutation *string         `json:"depends_on_mutation_id,omitempty"`
	FieldMask         []string        `json:"field_mask,omitempty"`
	Payload           json.RawMessage `json:"payload"`
}

type BatchResult struct {
	SchemaVersion string           `json:"schema_version"`
	Results       []MutationResult `json:"results"`
}

type MutationResult struct {
	MutationID string          `json:"mutation_id"`
	Status     string          `json:"status"`
	Entity     *EntityEnvelope `json:"entity,omitempty"`
	Error      *APIError       `json:"error,omitempty"`
}

type EntityEnvelope struct {
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Revision   int64           `json:"revision"`
	DeletedAt  *string         `json:"deleted_at,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

type APIError struct {
	SchemaVersion string `json:"schema_version"`
	Type          string `json:"type"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
}
