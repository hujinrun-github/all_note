package mobilev2service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/storage"
)

type dependencyResolution int

const (
	dependencyReady dependencyResolution = iota
	dependencyPending
	dependencyRejected
)

func resolveCommandDependencies(
	ctx context.Context,
	ledger *mobilev2command.SQLLedger,
	runner storage.TenantSQLRunner,
	envelope mobilev2command.Envelope,
) (mobilev2command.Envelope, dependencyResolution, error) {
	if ledger == nil || runner == nil {
		return envelope, dependencyPending, errors.New("mobile-v2 dependency ledger is unavailable")
	}
	receipts := make(map[string]mobilev2command.Receipt)
	load := func(commandID string) (mobilev2command.Receipt, bool, error) {
		if receipt, exists := receipts[commandID]; exists {
			return receipt, true, nil
		}
		receipt, found, err := ledger.LookupOnRunner(
			ctx, runner, envelope.WorkspaceID, envelope.OriginDeviceClientID, commandID,
		)
		if err != nil || !found {
			return mobilev2command.Receipt{}, found, err
		}
		receipts[commandID] = receipt
		return receipt, true, nil
	}

	if envelope.DependsOnCommandID != nil {
		receipt, found, err := load(*envelope.DependsOnCommandID)
		if err != nil {
			return envelope, dependencyPending, err
		}
		if !found {
			return envelope, dependencyPending, nil
		}
		if receipt.Status != mobilev2command.StatusApplied && receipt.Status != mobilev2command.StatusNoOp {
			return envelope, dependencyRejected, nil
		}
		if err := resolveEnvelopeTargets(&envelope, receipt); err != nil {
			return envelope, dependencyRejected, nil
		}
	}

	revisions := []struct {
		name     string
		revision **mobilev2command.EnvelopeExpectedRevision
	}{
		{name: "entity", revision: &envelope.Expected.EntityRevision},
		{name: "old_project", revision: &envelope.Expected.OldProjectRevision},
		{name: "new_project", revision: &envelope.Expected.NewProjectRevision},
		{name: "project", revision: &envelope.Expected.ProjectRevision},
		{name: "task", revision: &envelope.Expected.TaskRevision},
		{name: "schedule", revision: &envelope.Expected.ScheduleRevision},
		{name: "occurrence", revision: &envelope.Expected.OccurrenceRevision},
	}
	for _, candidate := range revisions {
		if *candidate.revision == nil || (*candidate.revision).Source != "from_dependency_receipt" {
			continue
		}
		dependencyID := (*candidate.revision).DependencyCommandID
		if dependencyID == nil {
			return envelope, dependencyRejected, nil
		}
		receipt, found, err := load(*dependencyID)
		if err != nil {
			return envelope, dependencyPending, err
		}
		if !found {
			return envelope, dependencyPending, nil
		}
		if receipt.Status != mobilev2command.StatusApplied && receipt.Status != mobilev2command.StatusNoOp {
			return envelope, dependencyRejected, nil
		}
		value, found := dependencyRevision(receipt, candidate.name, envelope)
		if !found {
			return envelope, dependencyRejected, nil
		}
		*candidate.revision = exactEnvelopeRevision(value)
	}
	for entityID, revision := range envelope.Expected.OccurrenceRevisions {
		if revision.Source != "from_dependency_receipt" {
			continue
		}
		if revision.DependencyCommandID == nil {
			return envelope, dependencyRejected, nil
		}
		receipt, found, err := load(*revision.DependencyCommandID)
		if err != nil {
			return envelope, dependencyPending, err
		}
		if !found {
			return envelope, dependencyPending, nil
		}
		value, found := affectedRevision(receipt, "task_occurrence", entityID)
		if !found {
			return envelope, dependencyRejected, nil
		}
		envelope.Expected.OccurrenceRevisions[entityID] = *exactEnvelopeRevision(value)
	}
	return envelope, dependencyReady, nil
}

func resolveEnvelopeTargets(
	envelope *mobilev2command.Envelope,
	dependency mobilev2command.Receipt,
) error {
	if envelope == nil {
		return mobilev2command.ErrInvalidCommandEnvelope
	}
	if !commandCreatesEntity(envelope.CommandType) && envelope.Target.ClientID != nil {
		entityType := commandTargetEntityType(envelope.CommandType)
		entityID, found := mappedEntityID(dependency, entityType, *envelope.Target.ClientID)
		if !found {
			return mobilev2command.ErrInvalidCommandEnvelope
		}
		envelope.Target = mobilev2command.EnvelopeTarget{EntityID: &entityID}
	}
	if envelope.CommandType != "task.create" {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return err
	}
	var target mobilev2command.EnvelopeTarget
	if err := json.Unmarshal(payload["project"], &target); err != nil {
		return err
	}
	if target.ClientID == nil {
		return nil
	}
	entityID, found := mappedEntityID(dependency, "project", *target.ClientID)
	if !found {
		return mobilev2command.ErrInvalidCommandEnvelope
	}
	target = mobilev2command.EnvelopeTarget{EntityID: &entityID}
	encoded, err := json.Marshal(target)
	if err != nil {
		return err
	}
	payload["project"] = encoded
	envelope.Payload, err = json.Marshal(payload)
	return err
}

func dependencyRevision(
	receipt mobilev2command.Receipt,
	name string,
	envelope mobilev2command.Envelope,
) (string, bool) {
	entityType := ""
	entityID := ""
	switch name {
	case "entity":
		entityType = commandTargetEntityType(envelope.CommandType)
		entityID = targetEntityID(envelope.Target)
	case "project", "old_project", "new_project":
		entityType = "project"
	case "task":
		entityType = "task"
		if !strings.HasPrefix(envelope.CommandType, "occurrence.") {
			entityID = targetEntityID(envelope.Target)
		}
	case "schedule":
		entityType = "task_schedule"
		if !strings.HasPrefix(envelope.CommandType, "occurrence.") {
			entityID = targetEntityID(envelope.Target)
		}
	case "occurrence":
		entityType = "task_occurrence"
		entityID = targetEntityID(envelope.Target)
	}
	return affectedRevision(receipt, entityType, entityID)
}

func affectedRevision(
	receipt mobilev2command.Receipt,
	entityType string,
	entityID string,
) (string, bool) {
	var match string
	for _, affected := range receipt.AffectedRevisions {
		if affected.EntityType != entityType || (entityID != "" && affected.EntityID != entityID) {
			continue
		}
		if match != "" && affected.Revision != match {
			return "", false
		}
		match = affected.Revision
	}
	return match, match != ""
}

func mappedEntityID(
	receipt mobilev2command.Receipt,
	entityType string,
	clientID string,
) (string, bool) {
	for _, mapping := range receipt.IdentityMappings {
		if mapping.EntityType != entityType || mapping.ClientID == nil ||
			mapping.EntityID == nil || *mapping.ClientID != clientID {
			continue
		}
		return *mapping.EntityID, strings.TrimSpace(*mapping.EntityID) != ""
	}
	return "", false
}

func exactEnvelopeRevision(value string) *mobilev2command.EnvelopeExpectedRevision {
	copy := value
	return &mobilev2command.EnvelopeExpectedRevision{Source: "exact", Value: &copy}
}

func commandCreatesEntity(commandType string) bool {
	switch commandType {
	case "note.create", "inbox.create", "voice.create", "project.create", "task.create":
		return true
	default:
		return false
	}
}

func commandTargetEntityType(commandType string) string {
	switch {
	case strings.HasPrefix(commandType, "note."):
		return "note"
	case strings.HasPrefix(commandType, "inbox."):
		return "inbox"
	case strings.HasPrefix(commandType, "voice"), strings.HasPrefix(commandType, "transcription."):
		return "voice_note"
	case strings.HasPrefix(commandType, "project."):
		return "project"
	case strings.HasPrefix(commandType, "task."), commandType == "schedule.reschedule-this-and-following":
		return "task"
	case strings.HasPrefix(commandType, "occurrence."):
		return "task_occurrence"
	default:
		return ""
	}
}
