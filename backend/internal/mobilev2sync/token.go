package mobilev2sync

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalidToken = errors.New("invalid mobile-v2 sync token")

type TokenBinding struct {
	WorkspaceID        string    `json:"workspace_id"`
	Scope              ScopeName `json:"scope"`
	ContractEpoch      string    `json:"contract_epoch"`
	RuntimeEpoch       string    `json:"runtime_epoch"`
	TaskModelVersion   int       `json:"task_model_version"`
	ProjectionTimeZone *string   `json:"projection_time_zone"`
	ScopeGeneration    string    `json:"scope_generation"`
}

type SnapshotPageToken struct {
	Binding        TokenBinding `json:"binding"`
	SnapshotID     string       `json:"snapshot_id"`
	AsOfSequence   string       `json:"as_of_sequence"`
	SnapshotCursor string       `json:"snapshot_cursor"`
	PageIndex      int          `json:"page_index"`
	ExpiresAt      int64        `json:"expires_at"`
}

type TokenCodec struct{ secret []byte }

func NewTokenCodec(secret string) TokenCodec { return TokenCodec{secret: []byte(secret)} }

func (codec TokenCodec) EncodeSnapshotPage(page SnapshotPageToken) (string, error) {
	if len(codec.secret) == 0 || !validBinding(page.Binding) || page.SnapshotID == "" || page.AsOfSequence == "" || page.SnapshotCursor == "" || page.PageIndex < 0 || page.ExpiresAt <= 0 {
		return "", ErrInvalidToken
	}
	payload, err := json.Marshal(struct {
		Version int               `json:"version"`
		Kind    string            `json:"kind"`
		Value   SnapshotPageToken `json:"value"`
	}{Version: 2, Kind: "snapshot-page", Value: page})
	if err != nil {
		return "", err
	}
	signature := codec.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (codec TokenCodec) DecodeSnapshotPage(token string, expected TokenBinding) (SnapshotPageToken, error) {
	if len(codec.secret) == 0 || !validBinding(expected) {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || subtle.ConstantTimeCompare(signature, codec.sign(payload)) != 1 {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	var envelope struct {
		Version int               `json:"version"`
		Kind    string            `json:"kind"`
		Value   SnapshotPageToken `json:"value"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Version != 2 || envelope.Kind != "snapshot-page" {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	page := envelope.Value
	if !equalBinding(page.Binding, expected) || page.SnapshotID == "" || page.AsOfSequence == "" || page.SnapshotCursor == "" || page.PageIndex < 0 || page.ExpiresAt <= 0 {
		return SnapshotPageToken{}, ErrInvalidToken
	}
	return page, nil
}

func (codec TokenCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, codec.secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func validBinding(binding TokenBinding) bool {
	if binding.WorkspaceID == "" || binding.ContractEpoch == "" || binding.RuntimeEpoch == "" || binding.TaskModelVersion < 2 || binding.ScopeGeneration == "" {
		return false
	}
	switch binding.Scope {
	case ScopeIPhoneContent, ScopeIPhoneTaskCore:
		return binding.ProjectionTimeZone == nil
	case ScopeIPhoneOccurrenceWindow, ScopeWatchOccurrenceWindow:
		return binding.ProjectionTimeZone != nil && *binding.ProjectionTimeZone != ""
	default:
		return false
	}
}

func equalBinding(left, right TokenBinding) bool {
	return left.WorkspaceID == right.WorkspaceID && left.Scope == right.Scope &&
		left.ContractEpoch == right.ContractEpoch && left.RuntimeEpoch == right.RuntimeEpoch &&
		left.TaskModelVersion == right.TaskModelVersion && left.ScopeGeneration == right.ScopeGeneration &&
		equalOptionalString(left.ProjectionTimeZone, right.ProjectionTimeZone)
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
