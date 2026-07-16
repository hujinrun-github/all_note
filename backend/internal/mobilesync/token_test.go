package mobilesync

import (
	"errors"
	"testing"
)

func TestSyncTokensAreOpaqueTamperEvidentAndWorkspaceScoped(t *testing.T) {
	codec := NewTokenCodec("synthetic-sync-token-secret")
	cursor, err := codec.EncodeCursor(SyncCursor{WorkspaceID: "workspace-a", Scope: "iphone", Position: 42})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.DecodeCursor(cursor, "workspace-a", "iphone")
	if err != nil || decoded.Position != 42 {
		t.Fatalf("decoded cursor=%+v err=%v", decoded, err)
	}
	if _, err := codec.DecodeCursor(cursor+"x", "workspace-a", "iphone"); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("tampered cursor error=%v", err)
	}
	if _, err := codec.DecodeCursor(cursor, "workspace-b", "iphone"); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("cross-workspace cursor error=%v", err)
	}
	if _, err := codec.DecodeCursor(cursor, "workspace-a", "watch"); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("cross-scope cursor error=%v", err)
	}
	watchCursor, err := codec.EncodeCursor(SyncCursor{WorkspaceID: "workspace-a", Scope: "watch", Position: 9, TimeZone: "Asia/Shanghai"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeCursorBound(watchCursor, "workspace-a", "watch", "America/New_York"); !errors.Is(err, ErrInvalidSyncToken) {
		t.Fatalf("cross-timezone cursor error=%v", err)
	}

	pageToken, err := codec.EncodeSnapshotPage(SnapshotPageToken{
		WorkspaceID: "workspace-a", Scope: "iphone", SessionID: "session-a", Offset: 7, ExpiresAt: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := codec.DecodeSnapshotPage(pageToken, "workspace-a", "iphone")
	if err != nil || page.SessionID != "session-a" || page.Offset != 7 || page.ExpiresAt != 99 {
		t.Fatalf("decoded page=%+v err=%v", page, err)
	}
}
