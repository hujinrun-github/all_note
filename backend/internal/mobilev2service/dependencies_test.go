package mobilev2service

import (
	"testing"

	"github.com/hujinrun/flowspace/internal/mobilev2command"
)

func TestDependencyRevisionForOccurrenceCommandUsesAggregateReceiptRevisions(t *testing.T) {
	occurrenceID := "occurrence-1"
	envelope := mobilev2command.Envelope{
		CommandType: "occurrence.complete",
		Target: mobilev2command.EnvelopeTarget{
			EntityID: &occurrenceID,
		},
	}
	receipt := mobilev2command.Receipt{
		AffectedRevisions: []mobilev2command.AffectedRevision{
			{EntityType: "task", EntityID: "task-1", Revision: "8"},
			{EntityType: "task_schedule", EntityID: "task-1", Revision: "5"},
			{EntityType: "task_occurrence", EntityID: occurrenceID, Revision: "3"},
		},
	}

	tests := []struct {
		name string
		want string
	}{
		{name: "task", want: "8"},
		{name: "schedule", want: "5"},
		{name: "occurrence", want: "3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := dependencyRevision(receipt, test.name, envelope)
			if !found || got != test.want {
				t.Fatalf("dependencyRevision(%q) = %q, %v; want %q, true", test.name, got, found, test.want)
			}
		})
	}
}
