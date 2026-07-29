package mobilev2command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
)

var (
	ErrInvalidCommandEnvelope = errors.New("invalid mobile-v2 command envelope")
	sha256DigestPattern       = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type EnvelopeTarget struct {
	EntityID *string `json:"entity_id"`
	ClientID *string `json:"client_id"`
}

type EnvelopeExpectedRevision struct {
	Source              string  `json:"source"`
	Value               *string `json:"value"`
	DependencyCommandID *string `json:"dependency_command_id"`
}

type EnvelopeExpectedRevisions struct {
	EntityRevision      *EnvelopeExpectedRevision           `json:"entity_revision"`
	OldProjectRevision  *EnvelopeExpectedRevision           `json:"old_project_revision"`
	NewProjectRevision  *EnvelopeExpectedRevision           `json:"new_project_revision"`
	ProjectRevision     *EnvelopeExpectedRevision           `json:"project_revision"`
	TaskRevision        *EnvelopeExpectedRevision           `json:"task_revision"`
	ScheduleRevision    *EnvelopeExpectedRevision           `json:"schedule_revision"`
	OccurrenceRevision  *EnvelopeExpectedRevision           `json:"occurrence_revision"`
	OccurrenceRevisions map[string]EnvelopeExpectedRevision `json:"occurrence_revisions"`
}

type Envelope struct {
	CommandID                 string                    `json:"command_id"`
	RequestDigest             string                    `json:"request_digest"`
	OriginDeviceClientID      string                    `json:"origin_device_client_id"`
	ForwardedByDeviceClientID *string                   `json:"forwarded_by_device_client_id"`
	WorkspaceID               string                    `json:"workspace_id"`
	CommandType               string                    `json:"command_type"`
	Target                    EnvelopeTarget            `json:"target"`
	CreatedRuntimeEpoch       string                    `json:"created_runtime_epoch"`
	Expected                  EnvelopeExpectedRevisions `json:"expected"`
	DependsOnCommandID        *string                   `json:"depends_on_command_id"`
	SupersedesCommandID       *string                   `json:"supersedes_command_id"`
	Payload                   json.RawMessage           `json:"payload"`

	Raw                   json.RawMessage `json:"-"`
	ExpectedRevisionNames []string        `json:"-"`
}

func ParseEnvelope(raw json.RawMessage, authenticatedWorkspaceID string) (Envelope, error) {
	if err := validateRequiredEnvelopeFields(raw); err != nil {
		return Envelope{}, err
	}
	var envelope Envelope
	if err := decodeStrictEnvelope(raw, &envelope); err != nil {
		return Envelope{}, invalidEnvelope(err.Error())
	}
	envelope.Raw = append(json.RawMessage(nil), raw...)
	envelope.WorkspaceID = strings.TrimSpace(envelope.WorkspaceID)
	envelope.CommandType = strings.TrimSpace(envelope.CommandType)
	envelope.CreatedRuntimeEpoch = strings.TrimSpace(envelope.CreatedRuntimeEpoch)
	if envelope.WorkspaceID == "" || envelope.WorkspaceID != strings.TrimSpace(authenticatedWorkspaceID) {
		return Envelope{}, invalidEnvelope("workspace_id does not match the authenticated workspace")
	}
	if !validUUID(envelope.CommandID) || !validUUID(envelope.OriginDeviceClientID) {
		return Envelope{}, invalidEnvelope("command_id and origin_device_client_id must be UUIDs")
	}
	if envelope.ForwardedByDeviceClientID != nil && !validUUID(*envelope.ForwardedByDeviceClientID) {
		return Envelope{}, invalidEnvelope("forwarded_by_device_client_id must be a UUID")
	}
	if err := validateOptionalUUID(envelope.DependsOnCommandID); err != nil {
		return Envelope{}, invalidEnvelope("depends_on_command_id must be a UUID or null")
	}
	if err := validateOptionalUUID(envelope.SupersedesCommandID); err != nil {
		return Envelope{}, invalidEnvelope("supersedes_command_id must be a UUID or null")
	}
	epoch, err := strconv.ParseUint(envelope.CreatedRuntimeEpoch, 10, 63)
	if err != nil || epoch < 1 {
		return Envelope{}, invalidEnvelope("created_runtime_epoch must be a positive bigint string")
	}
	if !sha256DigestPattern.MatchString(envelope.RequestDigest) {
		return Envelope{}, invalidEnvelope("request_digest must be a lowercase SHA-256 digest")
	}
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil || digest != envelope.RequestDigest {
		return Envelope{}, ErrRequestDigestMismatch
	}
	if err := validateTarget(envelope.Target); err != nil {
		return Envelope{}, err
	}
	if len(envelope.Payload) == 0 || envelope.Payload[0] != '{' {
		return Envelope{}, invalidEnvelope("payload must be an object")
	}
	names, err := validateExpectedRevisions(envelope.Expected)
	if err != nil {
		return Envelope{}, err
	}
	if err := ValidateExpectedRevisions(envelope.CommandType, names); err != nil {
		return Envelope{}, err
	}
	envelope.ExpectedRevisionNames = names
	return envelope, nil
}

func validateRequiredEnvelopeFields(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return invalidEnvelope(err.Error())
	}
	required := []string{
		"command_id", "request_digest", "origin_device_client_id", "workspace_id", "command_type",
		"target", "created_runtime_epoch", "expected", "depends_on_command_id",
		"supersedes_command_id", "payload",
	}
	for _, name := range required {
		if _, exists := object[name]; !exists {
			return invalidEnvelope(name + " is required")
		}
	}
	var target map[string]json.RawMessage
	if err := json.Unmarshal(object["target"], &target); err != nil {
		return invalidEnvelope("target must be an object")
	}
	for _, name := range []string{"entity_id", "client_id"} {
		if _, exists := target[name]; !exists {
			return invalidEnvelope("target." + name + " is required")
		}
	}
	return nil
}

func (envelope Envelope) LedgerCommand() Command {
	return Command{
		WorkspaceID: envelope.WorkspaceID, OriginDeviceClientID: envelope.OriginDeviceClientID,
		CommandID: envelope.CommandID, RequestDigest: envelope.RequestDigest,
		CommandType: envelope.CommandType, CreatedRuntimeEpoch: envelope.CreatedRuntimeEpoch,
		ExpectedRevisionNames: append([]string(nil), envelope.ExpectedRevisionNames...),
		RawEnvelope:           append([]byte(nil), envelope.Raw...),
		ForwardedByDeviceID:   cloneOptionalString(envelope.ForwardedByDeviceClientID),
	}
}

func (expected EnvelopeExpectedRevisions) Exact(name string) (int64, error) {
	revision := expected.named(name)
	if revision == nil || revision.Source != "exact" || revision.Value == nil {
		return 0, ErrExpectedRevisionRequired
	}
	value, err := strconv.ParseInt(*revision.Value, 10, 64)
	if err != nil || value < 1 {
		return 0, ErrExpectedRevisionRequired
	}
	return value, nil
}

func (expected EnvelopeExpectedRevisions) ExactOccurrences() (map[string]int64, error) {
	if expected.OccurrenceRevisions == nil {
		return nil, ErrExpectedRevisionRequired
	}
	result := make(map[string]int64, len(expected.OccurrenceRevisions))
	for entityID, revision := range expected.OccurrenceRevisions {
		entityID = strings.TrimSpace(entityID)
		if entityID == "" || revision.Source != "exact" || revision.Value == nil {
			return nil, ErrExpectedRevisionRequired
		}
		value, err := strconv.ParseInt(*revision.Value, 10, 64)
		if err != nil || value < 1 {
			return nil, ErrExpectedRevisionRequired
		}
		result[entityID] = value
	}
	return result, nil
}

func (expected EnvelopeExpectedRevisions) named(name string) *EnvelopeExpectedRevision {
	switch name {
	case "entity":
		return expected.EntityRevision
	case "old_project":
		return expected.OldProjectRevision
	case "new_project":
		return expected.NewProjectRevision
	case "project":
		return expected.ProjectRevision
	case "task":
		return expected.TaskRevision
	case "schedule":
		return expected.ScheduleRevision
	case "occurrence":
		return expected.OccurrenceRevision
	default:
		return nil
	}
}

func validateTarget(target EnvelopeTarget) error {
	hasEntityID := target.EntityID != nil && strings.TrimSpace(*target.EntityID) != ""
	hasClientID := target.ClientID != nil && strings.TrimSpace(*target.ClientID) != ""
	if hasEntityID == hasClientID {
		return invalidEnvelope("target must contain exactly one non-null identity")
	}
	if target.EntityID != nil && !hasEntityID {
		return invalidEnvelope("target.entity_id must be non-empty or null")
	}
	if target.ClientID != nil && (!hasClientID || !validUUID(*target.ClientID)) {
		return invalidEnvelope("target.client_id must be a UUID or null")
	}
	return nil
}

func validateExpectedRevisions(expected EnvelopeExpectedRevisions) ([]string, error) {
	candidates := []struct {
		name     string
		revision *EnvelopeExpectedRevision
	}{
		{name: "entity", revision: expected.EntityRevision},
		{name: "old_project", revision: expected.OldProjectRevision},
		{name: "new_project", revision: expected.NewProjectRevision},
		{name: "project", revision: expected.ProjectRevision},
		{name: "task", revision: expected.TaskRevision},
		{name: "schedule", revision: expected.ScheduleRevision},
		{name: "occurrence", revision: expected.OccurrenceRevision},
	}
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.revision == nil {
			continue
		}
		if err := validateExpectedRevision(*candidate.revision); err != nil {
			return nil, err
		}
		names = append(names, candidate.name)
	}
	if expected.OccurrenceRevisions != nil {
		for entityID, revision := range expected.OccurrenceRevisions {
			if strings.TrimSpace(entityID) == "" {
				return nil, ErrExpectedRevisionRequired
			}
			if err := validateExpectedRevision(revision); err != nil {
				return nil, err
			}
		}
		names = append(names, "occurrences")
	}
	return names, nil
}

func validateExpectedRevision(revision EnvelopeExpectedRevision) error {
	switch revision.Source {
	case "exact":
		if revision.Value == nil || revision.DependencyCommandID != nil {
			return ErrExpectedRevisionRequired
		}
		value, err := strconv.ParseUint(*revision.Value, 10, 63)
		if err != nil || value < 1 {
			return ErrExpectedRevisionRequired
		}
	case "from_dependency_receipt":
		if revision.Value != nil || revision.DependencyCommandID == nil || !validUUID(*revision.DependencyCommandID) {
			return ErrExpectedRevisionRequired
		}
	default:
		return ErrExpectedRevisionRequired
	}
	return nil
}

func validateOptionalUUID(value *string) error {
	if value == nil {
		return nil
	}
	if !validUUID(*value) {
		return ErrInvalidCommandEnvelope
	}
	return nil
}

func validUUID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed.String() == strings.ToLower(strings.TrimSpace(value))
}

func decodeStrictEnvelope(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("command body contains a trailing JSON value")
		}
		return err
	}
	return nil
}

func invalidEnvelope(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidCommandEnvelope, message)
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
