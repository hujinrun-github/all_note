package mobilev2command

import (
	"context"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
)

func TestMTDV2Contract003ExpectedRevisionMatrixRejectsPartialAggregates(t *testing.T) {
	tests := []struct {
		command string
		got     []string
		valid   bool
	}{
		{command: "note.create", valid: true},
		{command: "note.update", got: []string{"entity"}, valid: true},
		{command: "note.update", valid: false},
		{command: "voice.create", valid: true},
		{command: "voice.update", got: []string{"entity"}, valid: true},
		{command: "voice.update", valid: false},
		{command: "transcription.retry", got: []string{"entity"}, valid: true},
		{command: "project.create", valid: true},
		{command: "project.update", got: []string{"project"}, valid: true},
		{command: "project.update", valid: false},
		{command: "occurrence.complete", got: []string{"task", "schedule", "occurrence"}, valid: true},
		{command: "occurrence.complete", got: []string{"occurrence"}, valid: false},
		{command: "schedule.reschedule-this-and-following", got: []string{"task", "schedule"}, valid: true},
		{command: "schedule.reschedule-this-and-following", got: []string{"task", "occurrence"}, valid: false},
	}
	for _, test := range tests {
		err := ValidateExpectedRevisions(test.command, test.got)
		if test.valid && err != nil {
			t.Errorf("%s %v rejected: %v", test.command, test.got, err)
		}
		if !test.valid && !errors.Is(err, ErrExpectedRevisionRequired) {
			t.Errorf("%s %v error = %v", test.command, test.got, err)
		}
	}
}

func TestMTDV2Contract008And009ReceiptChangeAndWatchRelayAreAtomicAndIdempotent(t *testing.T) {
	engine := NewEngine("8")
	command := commandFixture(t, "8")
	applyCount := 0
	response, err := engine.Execute(context.Background(), command, func(context.Context) (DomainResult, error) {
		applyCount++
		return DomainResult{
			Status:            StatusApplied,
			IdentityMappings:  []IdentityMapping{{EntityType: "task_occurrence", ClientID: stringRef("occ-client"), EntityID: stringRef("occ-server")}},
			AffectedRevisions: []AffectedRevision{{EntityType: "task_occurrence", EntityID: "occ-server", Revision: "7"}},
			AfterImages:       [][]byte{[]byte(`{"entity_type":"task_occurrence","entity_id":"occ-server"}`)},
		}, nil
	})
	if err != nil || response.Receipt == nil || response.Replayed || response.Receipt.CommitSequence != 1 {
		t.Fatalf("first response=%#v err=%v", response, err)
	}
	changes := engine.ChangesAfter(0)
	if len(changes) != 1 || changes[0].Receipt.CommandID != command.CommandID || changes[0].OriginDeviceClientID != command.OriginDeviceClientID || len(changes[0].AfterImages) != 1 {
		t.Fatalf("atomic change batch = %#v", changes)
	}

	forwarder := "iphone-device"
	command.ForwardedByDeviceID = &forwarder
	replayed, err := engine.Execute(context.Background(), command, func(context.Context) (DomainResult, error) {
		applyCount++
		return DomainResult{}, errors.New("must not execute replay")
	})
	if err != nil || replayed.Receipt == nil || !replayed.Replayed || applyCount != 1 {
		t.Fatalf("relay replay=%#v applyCount=%d err=%v", replayed, applyCount, err)
	}
	if replayed.Receipt.OriginDeviceClientID != "watch-device" {
		t.Fatalf("relay rewrote origin to %q", replayed.Receipt.OriginDeviceClientID)
	}
}

func TestMTDV2Contract010And011ReceiptFirstEpochRecoveryAndPermanentLedger(t *testing.T) {
	engine := NewEngine("8")
	for index, status := range []ResultStatus{StatusApplied, StatusNoOp, StatusConflict, StatusRejected} {
		command := commandFixture(t, "7")
		command.CommandID = command.CommandID + string(rune('a'+index))
		command.RawEnvelope = commandEnvelope(command.CommandID, "7")
		command.RequestDigest = mustDigest(t, command.RawEnvelope)
		engineForReceipt := NewEngine("7")
		first, err := engineForReceipt.Execute(context.Background(), command, func(context.Context) (DomainResult, error) {
			return DomainResult{Status: status}, nil
		})
		if err != nil || first.Receipt == nil {
			t.Fatalf("store terminal %s: response=%#v err=%v", status, first, err)
		}
		engineForReceipt.runtimeEpoch = "8"
		replay, err := engineForReceipt.Execute(context.Background(), command, func(context.Context) (DomainResult, error) {
			return DomainResult{}, errors.New("old epoch receipt must win")
		})
		if err != nil || replay.Receipt == nil || replay.Receipt.Status != status || !replay.Replayed {
			t.Fatalf("old epoch terminal %s replay=%#v err=%v", status, replay, err)
		}
		engineForReceipt.CompactChanges(99)
		if _, found := engineForReceipt.Lookup(command.WorkspaceID, command.OriginDeviceClientID, command.CommandID); !found {
			t.Fatalf("change compaction removed terminal %s ledger", status)
		}
	}

	unknownOld := commandFixture(t, "7")
	if _, err := engine.Execute(context.Background(), unknownOld, func(context.Context) (DomainResult, error) { return DomainResult{}, nil }); !errors.Is(err, ErrStaleRuntimeEpoch) {
		t.Fatalf("unknown old epoch error = %v", err)
	}
	engine.MarkReceiptHistoryAmbiguous()
	if _, err := engine.Execute(context.Background(), unknownOld, func(context.Context) (DomainResult, error) { return DomainResult{}, nil }); !errors.Is(err, ErrReceiptHistoryAmbiguous) {
		t.Fatalf("ambiguous history error = %v", err)
	}

	retryEngine := NewEngine("8")
	retry := commandFixture(t, "8")
	response, err := retryEngine.Execute(context.Background(), retry, func(context.Context) (DomainResult, error) {
		return DomainResult{Status: StatusRetryLater}, nil
	})
	if err != nil || !response.RetryLater || response.Receipt != nil {
		t.Fatalf("retry-later response=%#v err=%v", response, err)
	}
	if _, found := retryEngine.Lookup(retry.WorkspaceID, retry.OriginDeviceClientID, retry.CommandID); found {
		t.Fatal("retry_later must not create terminal receipt")
	}
}

func TestMTDV2Contract017WorkspaceModeRejectsShadowAndCutoverWrites(t *testing.T) {
	tests := []struct {
		mode string
		want error
	}{
		{mode: "v2-active"},
		{mode: "legacy-active", want: ErrWorkspaceModeForbidsCommand},
		{mode: "v2-shadow-readonly", want: ErrWorkspaceModeForbidsCommand},
		{mode: "v2-cutover-migrating", want: ErrWorkspaceModeForbidsCommand},
		{mode: "upgrade-required", want: ErrUpgradeRequired},
		{mode: "unknown", want: ErrWorkspaceModeForbidsCommand},
	}
	for _, test := range tests {
		err := ValidateWorkspaceCommandMode(test.mode)
		if !errors.Is(err, test.want) {
			t.Errorf("mode %q error = %v, want %v", test.mode, err, test.want)
		}
	}
}

func commandFixture(t *testing.T, epoch string) Command {
	t.Helper()
	raw := commandEnvelope("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", epoch)
	return Command{
		WorkspaceID: "workspace-1", OriginDeviceClientID: "watch-device",
		CommandID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RequestDigest: mustDigest(t, raw),
		CommandType: "occurrence.complete", CreatedRuntimeEpoch: epoch,
		ExpectedRevisionNames: []string{"task", "schedule", "occurrence"}, RawEnvelope: raw,
	}
}

func commandEnvelope(commandID, epoch string) []byte {
	return []byte(`{"command_id":"` + commandID + `","request_digest":"sha256:ignored-by-digest","origin_device_client_id":"watch-device","workspace_id":"workspace-1","command_type":"occurrence.complete","target":{"entity_id":"occ-1","client_id":null},"created_runtime_epoch":"` + epoch + `","expected":{"task_revision":{"source":"exact","value":"6"},"schedule_revision":{"source":"exact","value":"2"},"occurrence_revision":{"source":"exact","value":"7"}},"depends_on_command_id":null,"supersedes_command_id":null,"payload":{}}`)
}

func mustDigest(t *testing.T, raw []byte) string {
	t.Helper()
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func stringRef(value string) *string { return &value }
