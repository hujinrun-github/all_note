package mobilesync

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCanonicalRequestHashNormalizesJSONAndFieldMask(t *testing.T) {
	base := int64(4)
	left, err := CanonicalRequestHash(MutationInput{
		MutationID:   "11111111-1111-4111-8111-111111111111",
		Operation:    "note.update",
		EntityID:     "22222222-2222-4222-8222-222222222222",
		BaseRevision: &base,
		FieldMask:    []string{"title", "body", "title"},
		Payload:      json.RawMessage(`{"title":"New","body":"Text"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalRequestHash(MutationInput{
		MutationID:   "11111111-1111-4111-8111-111111111111",
		Operation:    "note.update",
		EntityID:     "22222222-2222-4222-8222-222222222222",
		BaseRevision: &base,
		FieldMask:    []string{"body", "title"},
		Payload:      json.RawMessage(`{"body":"Text","title":"New"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if left == "" || left != right {
		t.Fatalf("canonical hashes left=%q right=%q", left, right)
	}
}

func TestCanonicalRequestHashDistinguishesOmittedAndExplicitNull(t *testing.T) {
	omitted, err := CanonicalRequestHash(MutationInput{
		MutationID: "33333333-3333-4333-8333-333333333333",
		Operation:  "note.update",
		EntityID:   "44444444-4444-4444-8444-444444444444",
		Payload:    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	explicitNull, err := CanonicalRequestHash(MutationInput{
		MutationID: "33333333-3333-4333-8333-333333333333",
		Operation:  "note.update",
		EntityID:   "44444444-4444-4444-8444-444444444444",
		Payload:    json.RawMessage(`{"body":null}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if omitted == explicitNull {
		t.Fatalf("omitted and explicit null produced the same hash %q", omitted)
	}
}

func TestApplyBatchRejectsLimitsBeforeStoreUse(t *testing.T) {
	mutations := make([]MutationInput, MaxBatchMutations+1)
	for i := range mutations {
		mutations[i] = MutationInput{MutationID: "mutation", Operation: "note.create", EntityID: "entity", Payload: json.RawMessage(`{}`)}
	}
	if _, err := ApplyBatch(t.Context(), nil, MutationBatch{ClientID: "client", Mutations: mutations}); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("ApplyBatch error = %v, want ErrBatchTooLarge", err)
	}
}
