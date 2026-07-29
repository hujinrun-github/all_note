package mobilev2sync

import (
	"errors"
	"testing"
)

func TestMTDV2Contract004And007PageTokenBindsEntireFixedView(t *testing.T) {
	timeZone := "Asia/Shanghai"
	binding := TokenBinding{
		WorkspaceID: "workspace-1", Scope: ScopeIPhoneOccurrenceWindow,
		ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 2,
		ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1",
	}
	codec := NewTokenCodec("test-mobile-v2-secret")
	token, err := codec.EncodeSnapshotPage(SnapshotPageToken{
		Binding: binding, SnapshotID: "snapshot-1", AsOfSequence: "42",
		SnapshotCursor: "cursor-42", PageIndex: 1, ExpiresAt: 2_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.DecodeSnapshotPage(token, binding)
	if err != nil || decoded.SnapshotID != "snapshot-1" || decoded.AsOfSequence != "42" || decoded.SnapshotCursor != "cursor-42" || decoded.PageIndex != 1 {
		t.Fatalf("decoded token=%#v err=%v", decoded, err)
	}
	if _, err := codec.DecodeSnapshotPage(token+"x", binding); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tamper error = %v", err)
	}

	mismatches := []TokenBinding{
		{WorkspaceID: "other", Scope: binding.Scope, ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 2, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: ScopeWatchOccurrenceWindow, ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 2, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: binding.Scope, ContractEpoch: "13", RuntimeEpoch: "8", TaskModelVersion: 2, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: binding.Scope, ContractEpoch: "12", RuntimeEpoch: "9", TaskModelVersion: 2, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: binding.Scope, ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 3, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: binding.Scope, ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 2, ProjectionTimeZone: stringPointer("UTC"), ScopeGeneration: "generation-1"},
		{WorkspaceID: binding.WorkspaceID, Scope: binding.Scope, ContractEpoch: "12", RuntimeEpoch: "8", TaskModelVersion: 2, ProjectionTimeZone: &timeZone, ScopeGeneration: "generation-2"},
	}
	for _, mismatch := range mismatches {
		if _, err := codec.DecodeSnapshotPage(token, mismatch); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("mismatched binding %#v error = %v", mismatch, err)
		}
	}
}

func TestChangeCursorBindsScopeGenerationAndEpoch(t *testing.T) {
	binding := TokenBinding{
		WorkspaceID: "workspace-1", Scope: ScopeIPhoneTaskCore,
		ContractEpoch: "2", RuntimeEpoch: "8", TaskModelVersion: 2,
		ScopeGeneration: "task-core-v2",
	}
	codec := NewTokenCodec("test-mobile-v2-secret")
	token, err := codec.EncodeChangeCursor(ChangeCursorToken{Binding: binding, Sequence: "42"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.DecodeChangeCursor(token, binding)
	if err != nil || decoded.Sequence != "42" {
		t.Fatalf("decoded cursor=%#v err=%v", decoded, err)
	}
	changed := binding
	changed.RuntimeEpoch = "9"
	if _, err := codec.DecodeChangeCursor(token, changed); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("epoch mismatch error = %v", err)
	}
	if _, err := codec.EncodeChangeCursor(ChangeCursorToken{Binding: binding, Sequence: "01"}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("leading-zero sequence error = %v", err)
	}
}
