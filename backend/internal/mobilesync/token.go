package mobilesync

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalidSyncToken = errors.New("invalid mobile sync token")

type SyncCursor struct {
	WorkspaceID string `json:"workspace_id"`
	Scope       string `json:"scope"`
	Position    int64  `json:"position"`
	TimeZone    string `json:"time_zone"`
	Protocol    string `json:"protocol"`
}

type SnapshotPageToken struct {
	WorkspaceID string `json:"workspace_id"`
	Scope       string `json:"scope"`
	SessionID   string `json:"session_id"`
	Offset      int64  `json:"offset"`
	ExpiresAt   int64  `json:"expires_at"`
	TimeZone    string `json:"time_zone"`
	Protocol    string `json:"protocol"`
}

type TokenCodec struct {
	secret []byte
}

func NewTokenCodec(secret string) TokenCodec {
	return TokenCodec{secret: []byte(secret)}
}

func (c TokenCodec) EncodeCursor(cursor SyncCursor) (string, error) {
	normalizeTokenBinding(&cursor.TimeZone, &cursor.Protocol)
	if cursor.WorkspaceID == "" || !validSyncScope(cursor.Scope) || cursor.Position < 0 {
		return "", ErrInvalidSyncToken
	}
	return c.encode("cursor", cursor)
}

func (c TokenCodec) DecodeCursor(token, workspaceID, scope string) (SyncCursor, error) {
	var cursor SyncCursor
	if err := c.decode(token, "cursor", &cursor); err != nil {
		return SyncCursor{}, err
	}
	if cursor.WorkspaceID != workspaceID || cursor.Scope != scope || cursor.Position < 0 || !validTokenBinding(cursor.TimeZone, cursor.Protocol) {
		return SyncCursor{}, ErrInvalidSyncToken
	}
	return cursor, nil
}

func (c TokenCodec) DecodeCursorBound(token, workspaceID, scope, timeZone string) (SyncCursor, error) {
	cursor, err := c.DecodeCursor(token, workspaceID, scope)
	if err != nil || cursor.TimeZone != timeZone || cursor.Protocol != "mobile-v1" {
		return SyncCursor{}, ErrInvalidSyncToken
	}
	return cursor, nil
}

func (c TokenCodec) EncodeSnapshotPage(page SnapshotPageToken) (string, error) {
	normalizeTokenBinding(&page.TimeZone, &page.Protocol)
	if page.WorkspaceID == "" || !validSyncScope(page.Scope) || page.SessionID == "" || page.Offset < 0 || page.ExpiresAt <= 0 {
		return "", ErrInvalidSyncToken
	}
	return c.encode("snapshot", page)
}

func (c TokenCodec) DecodeSnapshotPage(token, workspaceID, scope string) (SnapshotPageToken, error) {
	var page SnapshotPageToken
	if err := c.decode(token, "snapshot", &page); err != nil {
		return SnapshotPageToken{}, err
	}
	if page.WorkspaceID != workspaceID || page.Scope != scope || page.SessionID == "" || page.Offset < 0 || page.ExpiresAt <= 0 || !validTokenBinding(page.TimeZone, page.Protocol) {
		return SnapshotPageToken{}, ErrInvalidSyncToken
	}
	return page, nil
}

func (c TokenCodec) DecodeSnapshotPageBound(token, workspaceID, scope, timeZone string) (SnapshotPageToken, error) {
	page, err := c.DecodeSnapshotPage(token, workspaceID, scope)
	if err != nil || page.TimeZone != timeZone || page.Protocol != "mobile-v1" {
		return SnapshotPageToken{}, ErrInvalidSyncToken
	}
	return page, nil
}

func (c TokenCodec) encode(kind string, value any) (string, error) {
	if len(c.secret) == 0 {
		return "", ErrInvalidSyncToken
	}
	payload, err := json.Marshal(struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		Value   any    `json:"value"`
	}{Version: 1, Kind: kind, Value: value})
	if err != nil {
		return "", err
	}
	signature := c.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c TokenCodec) decode(token, kind string, destination any) error {
	if len(c.secret) == 0 {
		return ErrInvalidSyncToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return ErrInvalidSyncToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ErrInvalidSyncToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || subtle.ConstantTimeCompare(signature, c.sign(payload)) != 1 {
		return ErrInvalidSyncToken
	}
	var envelope struct {
		Version int             `json:"version"`
		Kind    string          `json:"kind"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Version != 1 || envelope.Kind != kind {
		return ErrInvalidSyncToken
	}
	if err := json.Unmarshal(envelope.Value, destination); err != nil {
		return ErrInvalidSyncToken
	}
	return nil
}

func (c TokenCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func validSyncScope(scope string) bool {
	return scope == "iphone" || scope == "watch"
}

func normalizeTokenBinding(timeZone, protocol *string) {
	if *timeZone == "" {
		*timeZone = "UTC"
	}
	if *protocol == "" {
		*protocol = "mobile-v1"
	}
}

func validTokenBinding(timeZone, protocol string) bool {
	if protocol != "mobile-v1" {
		return false
	}
	_, err := time.LoadLocation(timeZone)
	return err == nil
}
