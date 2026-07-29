package mobilev2command

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
)

func TestParseEnvelopeValidatesIdentityDigestAndExpectedRevisions(t *testing.T) {
	raw := validEnvelope(t, "occurrence.complete", `"target":{"entity_id":"occurrence-1","client_id":null}`,
		`"expected":{"task_revision":{"source":"exact","value":"6","dependency_command_id":null},"schedule_revision":{"source":"exact","value":"2","dependency_command_id":null},"occurrence_revision":{"source":"exact","value":"7","dependency_command_id":null}}`)
	envelope, err := ParseEnvelope(raw, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if envelope.CommandType != "occurrence.complete" || len(envelope.ExpectedRevisionNames) != 3 {
		t.Fatalf("parsed envelope = %#v", envelope)
	}
	if taskRevision, err := envelope.Expected.Exact("task"); err != nil || taskRevision != 6 {
		t.Fatalf("task revision = %d, err=%v", taskRevision, err)
	}
	command := envelope.LedgerCommand()
	if command.WorkspaceID != "workspace-1" || command.RequestDigest != envelope.RequestDigest ||
		command.CommandID != envelope.CommandID {
		t.Fatalf("ledger command = %#v", command)
	}
}

func TestParseEnvelopeRejectsUnknownFieldsWorkspaceMismatchAndDigestMismatch(t *testing.T) {
	valid := validEnvelope(t, "project.create", `"target":{"entity_id":null,"client_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`,
		`"expected":{}`)
	unknown := append(json.RawMessage(nil), valid...)
	unknown = json.RawMessage(strings.Replace(string(unknown), `"payload":{}`, `"unknown":true,"payload":{}`, 1))
	if _, err := ParseEnvelope(unknown, "workspace-1"); !errors.Is(err, ErrInvalidCommandEnvelope) {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := ParseEnvelope(valid, "workspace-2"); !errors.Is(err, ErrInvalidCommandEnvelope) {
		t.Fatalf("workspace mismatch error = %v", err)
	}
	mismatched := json.RawMessage(strings.Replace(string(valid), `"request_digest":"sha256:`, `"request_digest":"sha256:f`, 1))
	if _, err := ParseEnvelope(mismatched, "workspace-1"); !errors.Is(err, ErrInvalidCommandEnvelope) &&
		!errors.Is(err, ErrRequestDigestMismatch) {
		t.Fatalf("digest mismatch error = %v", err)
	}
}

func TestParseEnvelopeRejectsInvalidExpectedRevisionSet(t *testing.T) {
	raw := validEnvelope(t, "task.create", `"target":{"entity_id":null,"client_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`,
		`"expected":{"task_revision":{"source":"exact","value":"1","dependency_command_id":null}}`)
	if _, err := ParseEnvelope(raw, "workspace-1"); !errors.Is(err, ErrExpectedRevisionRequired) {
		t.Fatalf("expected revision error = %v", err)
	}
}

func validEnvelope(t *testing.T, commandType, target, expected string) json.RawMessage {
	t.Helper()
	raw := json.RawMessage(`{
		"command_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"request_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"origin_device_client_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"workspace_id":"workspace-1",
		"command_type":"` + commandType + `",
		` + target + `,
		"created_runtime_epoch":"8",
		` + expected + `,
		"depends_on_command_id":null,
		"supersedes_command_id":null,
		"payload":{}
	}`)
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(strings.Replace(string(raw),
		"sha256:0000000000000000000000000000000000000000000000000000000000000000", digest, 1))
}
