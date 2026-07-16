package mobilesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func CanonicalRequestHash(input MutationInput) (string, error) {
	canonicalPayload, err := canonicalJSONObject(input.Payload)
	if err != nil {
		return "", err
	}
	fieldMask, err := canonicalFieldMask(input.FieldMask)
	if err != nil {
		return "", err
	}
	canonical := struct {
		MutationID   string          `json:"mutation_id"`
		Operation    string          `json:"operation"`
		EntityID     string          `json:"entity_id"`
		BaseRevision *int64          `json:"base_revision,omitempty"`
		FieldMask    []string        `json:"field_mask,omitempty"`
		Payload      json.RawMessage `json:"payload"`
	}{
		MutationID:   strings.TrimSpace(input.MutationID),
		Operation:    strings.TrimSpace(input.Operation),
		EntityID:     strings.TrimSpace(input.EntityID),
		BaseRevision: input.BaseRevision,
		FieldMask:    fieldMask,
		Payload:      canonicalPayload,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ApplyBatch(ctx context.Context, store storage.Store, batch MutationBatch) (*BatchResult, error) {
	if len(batch.Mutations) == 0 {
		return nil, fmt.Errorf("%w: mutations must not be empty", ErrInvalidBatch)
	}
	if len(batch.Mutations) > MaxBatchMutations {
		return nil, ErrBatchTooLarge
	}
	encoded, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBatch, err)
	}
	if len(encoded) > MaxBatchBytes {
		return nil, ErrBatchTooLarge
	}
	clientID := strings.TrimSpace(batch.ClientID)
	if _, err := uuid.Parse(clientID); err != nil {
		return nil, fmt.Errorf("%w: client_id must be a UUID", ErrInvalidBatch)
	}
	if store == nil {
		return nil, storage.ErrMobileSyncStorage
	}
	repository, err := storage.MobileSyncRepositoryFrom(store)
	if err != nil {
		return nil, err
	}

	response := &BatchResult{SchemaVersion: "mobile-v1", Results: make([]MutationResult, 0, len(batch.Mutations))}
	for _, input := range batch.Mutations {
		result := applyMutation(ctx, repository, clientID, input)
		response.Results = append(response.Results, result)
	}
	return response, nil
}

func applyMutation(ctx context.Context, repository storage.MobileSyncRepository, deviceClientID string, input MutationInput) MutationResult {
	mutationID := strings.TrimSpace(input.MutationID)
	if _, err := uuid.Parse(mutationID); err != nil {
		return rejectedMutation(mutationID, "invalid_mutation_id")
	}
	entityID := strings.TrimSpace(input.EntityID)
	if _, err := uuid.Parse(entityID); err != nil {
		return rejectedMutation(mutationID, "invalid_entity_id")
	}
	requestHash, err := CanonicalRequestHash(input)
	if err != nil {
		return rejectedMutation(mutationID, "invalid_payload")
	}
	canonicalPayload, err := canonicalJSONObject(input.Payload)
	if err != nil {
		return rejectedMutation(mutationID, "invalid_payload")
	}
	entityType, supported := mutationEntityType(input.Operation)
	if !supported {
		return rejectedMutation(mutationID, "unsupported_operation")
	}
	var result *model.MobileMutationResult
	if entityType == "note" {
		payload, decodeErr := decodeNotePayload(input.Payload)
		if decodeErr != nil {
			return rejectedMutation(mutationID, "invalid_payload")
		}
		result, err = repository.ApplyNoteMutation(ctx, model.MobileNoteMutation{
			MutationID:     mutationID,
			DeviceClientID: deviceClientID,
			EntityClientID: entityID,
			Operation:      input.Operation,
			BaseRevision:   input.BaseRevision,
			RequestSHA256:  requestHash,
			Payload:        payload,
		})
	} else {
		result, err = repository.ApplyEntityMutation(ctx, model.MobileEntityMutation{
			MutationID:     mutationID,
			DeviceClientID: deviceClientID,
			EntityType:     entityType,
			EntityClientID: entityID,
			Operation:      input.Operation,
			BaseRevision:   input.BaseRevision,
			RequestSHA256:  requestHash,
			Payload:        canonicalPayload,
		})
	}
	if err == nil {
		return mobileMutationResultToWire(result)
	}
	switch {
	case errors.Is(err, storage.ErrRevisionConflict):
		if input.BaseRevision == nil {
			return mutationError(mutationID, model.MobileMutationConflict, "revision_conflict")
		}
		conflictResult, conflictErr := repository.CreateConflict(ctx, model.CreateMobileSyncConflict{
			ConflictID: uuid.NewString(), MutationID: mutationID, DeviceClientID: deviceClientID,
			RequestSHA256: requestHash, EntityType: entityType, EntityClientID: entityID,
			Operation: input.Operation, BaseRevision: *input.BaseRevision, LocalPayload: canonicalPayload,
		})
		if conflictErr != nil {
			return mutationError(mutationID, model.MobileMutationConflict, "revision_conflict")
		}
		return mobileMutationResultToWire(conflictResult)
	case errors.Is(err, storage.ErrMutationIDReused):
		return rejectedMutation(mutationID, "mutation_id_reused")
	case errors.Is(err, storage.ErrMobileEntityGone):
		return rejectedMutation(mutationID, "entity_gone")
	case errors.Is(err, storage.ErrMobileEntityNotFound):
		return rejectedMutation(mutationID, "entity_not_found")
	default:
		return rejectedMutation(mutationID, "mutation_failed")
	}
}

func mobileMutationResultToWire(result *model.MobileMutationResult) MutationResult {
	wire := MutationResult{
		MutationID: result.MutationID, Status: result.Status, Entity: entityToWire(result.Entity),
	}
	if result.ErrorCode != "" {
		wire.Error = &APIError{
			SchemaVersion: "mobile-v1", Type: "error", Code: result.ErrorCode,
			Message: "mobile mutation was not applied", Retryable: false,
		}
	}
	return wire
}

func mutationEntityType(operation string) (string, bool) {
	switch operation {
	case model.MobileOperationNoteCreate, model.MobileOperationNoteUpdate, model.MobileOperationNoteDelete:
		return "note", true
	case "task.create", "task.update", "task.delete":
		return "task", true
	case "event.create", "event.update", "event.delete":
		return "event", true
	case "inbox.create", "inbox.update", "inbox.delete":
		return "inbox", true
	case "task_occurrence.complete", "task_occurrence.reopen":
		return "task_occurrence", true
	case "voice.create", "voice_audio.delete", "voice_note.delete":
		return "voice_note", true
	default:
		return "", false
	}
}

func canonicalJSONObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("payload must be an object")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("JSON contains multiple values")
	}
	return err
}

func canonicalFieldMask(input []string) ([]string, error) {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, value := range input {
		field := strings.TrimSpace(value)
		if field == "" {
			return nil, errors.New("field_mask contains an empty field")
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	sort.Strings(result)
	return result, nil
}

func decodeNotePayload(raw json.RawMessage) (model.MobileNotePayload, error) {
	canonical, err := canonicalJSONObject(raw)
	if err != nil {
		return model.MobileNotePayload{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return model.MobileNotePayload{}, err
	}
	allowed := map[string]bool{"title": true, "body": true, "folder_id": true, "tags": true}
	for field := range fields {
		if !allowed[field] {
			return model.MobileNotePayload{}, fmt.Errorf("unknown note field %q", field)
		}
	}
	payload := model.MobileNotePayload{}
	if value, ok := fields["title"]; ok {
		payload.Title, err = decodeNullableString(value)
		if err != nil {
			return model.MobileNotePayload{}, fmt.Errorf("title: %w", err)
		}
	}
	if value, ok := fields["body"]; ok {
		payload.Body, err = decodeNullableString(value)
		if err != nil {
			return model.MobileNotePayload{}, fmt.Errorf("body: %w", err)
		}
	}
	if value, ok := fields["folder_id"]; ok {
		payload.FolderID, err = decodeNullableString(value)
		if err != nil {
			return model.MobileNotePayload{}, fmt.Errorf("folder_id: %w", err)
		}
	}
	if value, ok := fields["tags"]; ok {
		payload.Tags, err = decodeTags(value)
		if err != nil {
			return model.MobileNotePayload{}, fmt.Errorf("tags: %w", err)
		}
	}
	return payload, nil
}

func decodeNullableString(raw json.RawMessage) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		empty := ""
		return &empty, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeTags(raw json.RawMessage) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		empty := "[]"
		return &empty, nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	value := string(encoded)
	return &value, nil
}

func rejectedMutation(mutationID, code string) MutationResult {
	return mutationError(mutationID, "rejected", code)
}

func mutationError(mutationID, status, code string) MutationResult {
	return MutationResult{
		MutationID: mutationID,
		Status:     status,
		Error: &APIError{
			SchemaVersion: "mobile-v1", Type: "error", Code: code,
			Message: "mobile mutation was not applied", Retryable: false,
		},
	}
}

func entityToWire(entity *model.MobileEntityEnvelope) *EntityEnvelope {
	if entity == nil {
		return nil
	}
	wire := &EntityEnvelope{
		EntityType: entity.EntityType, EntityID: entity.ClientID, Revision: entity.Revision, Payload: entity.Payload,
	}
	if entity.DeletedAt != nil {
		deletedAt := time.Unix(*entity.DeletedAt, 0).UTC().Format(time.RFC3339)
		wire.DeletedAt = &deletedAt
	}
	return wire
}
