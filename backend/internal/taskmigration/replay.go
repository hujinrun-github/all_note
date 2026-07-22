package taskmigration

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type ReplayEntityKind string

const (
	ReplayEntityProject ReplayEntityKind = "project"
	ReplayEntityTask    ReplayEntityKind = "task"
	// ReplayEntitySchedule is a v2 reconciliation target kind. It is not a
	// valid legacy outbox entity and validReplayEntityKind intentionally does
	// not accept it.
	ReplayEntitySchedule   ReplayEntityKind = "schedule"
	ReplayEntityRule       ReplayEntityKind = "rule"
	ReplayEntityOccurrence ReplayEntityKind = "occurrence"
	ReplayEntityEvent      ReplayEntityKind = "event"
	// Roadmap kinds are frozen-snapshot reconciliation identities. They are
	// intentionally not accepted by validReplayEntityKind because roadmap DML
	// is database-frozen during migration instead of entering the outbox.
	ReplayEntityRoadmap     ReplayEntityKind = "roadmap"
	ReplayEntityRoadmapNode ReplayEntityKind = "roadmap_node"
	ReplayEntityRoadmapEdge ReplayEntityKind = "roadmap_edge"
)

type ReplayOperation string

const (
	ReplayUpsert ReplayOperation = "upsert"
	ReplayDelete ReplayOperation = "delete"
)

type ReplayBlockCode string

const (
	ReplayBlockSequenceOrder ReplayBlockCode = "sequence_not_strictly_increasing"
	// ReplayBlockSequenceGap is retained for persisted error-code compatibility.
	// Workspace-scoped streams may legitimately contain gaps because the
	// database sequence is global across workspaces, so new reductions do not
	// emit this code.
	ReplayBlockSequenceGap     ReplayBlockCode = "sequence_gap"
	ReplayBlockLogicalVersion  ReplayBlockCode = "invalid_logical_version"
	ReplayBlockVersionConflict ReplayBlockCode = "logical_version_conflict"
	ReplayBlockEntityKind      ReplayBlockCode = "invalid_entity_kind"
	ReplayBlockOperation       ReplayBlockCode = "invalid_operation"
	ReplayBlockIdentity        ReplayBlockCode = "missing_source_identity"
	ReplayBlockMissingImage    ReplayBlockCode = "missing_replay_image"
	ReplayBlockDependencyOrder ReplayBlockCode = "invalid_dependency_order"
	ReplayBlockInvalidState    ReplayBlockCode = "invalid_replay_state"
)

type ReplayBlock struct {
	Code      ReplayBlockCode
	Reference string
	Detail    string
}

func (e *ReplayBlock) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reference)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Reference, e.Detail)
}

// ReplayImage is the normalized source after-image or delete before-image.
// The reducer treats it as opaque immutable data; provider adapters own the
// schema-specific decoding and persistence.
type ReplayImage map[string]string

type ReplayEntityKey struct {
	Kind     ReplayEntityKind
	SourceID string
}

type ReplayEvent struct {
	Sequence       int64
	EntityKind     ReplayEntityKind
	SourceID       string
	Operation      ReplayOperation
	LogicalVersion int64
	AfterImage     ReplayImage
	TombstoneImage ReplayImage
}

// ReplayLedgerEntry remains after a projection is deleted. Its logical
// version is the defense against a delayed snapshot or outbox upsert reviving
// an older source row.
type ReplayLedgerEntry struct {
	LogicalVersion int64
	Deleted        bool
}

type ReplayProjectionEntry struct {
	LogicalVersion int64
	Image          ReplayImage
}

// ReplayState contains only state that must be persisted transactionally by a
// later provider adapter. ReduceReplay itself performs no I/O.
type ReplayState struct {
	Watermark  int64
	Ledger     map[ReplayEntityKey]ReplayLedgerEntry
	Projection map[ReplayEntityKey]ReplayProjectionEntry
}

type ReplayStep struct {
	Sequence       int64
	Entity         ReplayEntityKey
	Operation      ReplayOperation
	LogicalVersion int64
	Image          ReplayImage
}

type ReplayPlan struct {
	FromWatermark int64
	ToWatermark   int64
	Steps         []ReplayStep
}

// ReduceReplay validates and reduces a strictly ordered unprocessed suffix of
// workspace-scoped outbox events. The database sequence is global, therefore
// events for one workspace may have gaps where other workspaces wrote events.
// Events already at or below the persisted watermark may be included again
// and are ignored, making retrying a fetched page idempotent.
//
// The returned state and plan must later be committed in one provider
// transaction. On any validation or ordering error this function returns the
// original state and a zero plan, so callers cannot accidentally persist a
// partial reduction.
func ReduceReplay(state ReplayState, events []ReplayEvent) (ReplayState, ReplayPlan, error) {
	if err := validateReplayState(state); err != nil {
		return state, ReplayPlan{}, err
	}
	if err := validateReplayEvents(state.Watermark, events); err != nil {
		return state, ReplayPlan{}, err
	}

	next := cloneReplayState(state)
	plan := ReplayPlan{FromWatermark: state.Watermark, ToWatermark: state.Watermark}
	for _, event := range events {
		if event.Sequence <= state.Watermark {
			continue
		}
		if err := reduceReplayEvent(&next, &plan, event); err != nil {
			return state, ReplayPlan{}, err
		}
		next.Watermark = event.Sequence
		plan.ToWatermark = event.Sequence
	}
	plan.Steps = normalizeReplaySteps(plan.Steps)
	if err := validateReplayDependencyOrder(plan.Steps); err != nil {
		return state, ReplayPlan{}, err
	}
	return next, plan, nil
}

// normalizeReplaySteps reduces all mutations for an entity in the fetched
// page to its final after-image/tombstone, then orders independent projection
// writes by dependency. Raw outbox chronology is not dependency ordered: an
// existing task may be updated before an unrelated project in the same page.
// Coalescing also prevents reordering a delete/recreate pair into the wrong
// final result.
func normalizeReplaySteps(steps []ReplayStep) []ReplayStep {
	if len(steps) < 2 {
		return steps
	}
	latest := make(map[ReplayEntityKey]ReplayStep, len(steps))
	for _, step := range steps {
		latest[step.Entity] = step
	}
	result := make([]ReplayStep, 0, len(latest))
	for _, step := range latest {
		step.Image = cloneReplayImage(step.Image)
		result = append(result, step)
	}
	sort.Slice(result, func(i, j int) bool {
		left := replayDependencyOrder(result[i].Entity.Kind, result[i].Operation)
		right := replayDependencyOrder(result[j].Entity.Kind, result[j].Operation)
		if left != right {
			return left < right
		}
		if result[i].Sequence != result[j].Sequence {
			return result[i].Sequence < result[j].Sequence
		}
		if result[i].Entity.Kind != result[j].Entity.Kind {
			return result[i].Entity.Kind < result[j].Entity.Kind
		}
		return result[i].Entity.SourceID < result[j].Entity.SourceID
	})
	return result
}

func validateReplayState(state ReplayState) error {
	if state.Watermark < 0 {
		return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: fmt.Sprint(state.Watermark), Detail: "negative watermark"}
	}
	for key, projection := range state.Projection {
		ledger, ok := state.Ledger[key]
		if !ok || ledger.Deleted || ledger.LogicalVersion != projection.LogicalVersion || projection.LogicalVersion <= 0 {
			return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: replayKeyReference(key), Detail: "projection and ledger disagree"}
		}
		if len(projection.Image) == 0 {
			return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: replayKeyReference(key), Detail: "projection image is empty"}
		}
	}
	for key, ledger := range state.Ledger {
		if !validReplayEntityKind(key.Kind) || strings.TrimSpace(key.SourceID) == "" || ledger.LogicalVersion <= 0 {
			return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: replayKeyReference(key), Detail: "invalid ledger entry"}
		}
		_, projected := state.Projection[key]
		if ledger.Deleted && projected {
			return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: replayKeyReference(key), Detail: "deleted ledger has projection"}
		}
		if !ledger.Deleted && !projected {
			return &ReplayBlock{Code: ReplayBlockInvalidState, Reference: replayKeyReference(key), Detail: "active ledger lacks projection"}
		}
	}
	return nil
}

func validateReplayEvents(_ int64, events []ReplayEvent) error {
	var previous int64
	for index, event := range events {
		if event.Sequence <= 0 {
			return &ReplayBlock{Code: ReplayBlockSequenceOrder, Reference: fmt.Sprint(event.Sequence), Detail: "sequence must be positive"}
		}
		if index > 0 && event.Sequence <= previous {
			return &ReplayBlock{Code: ReplayBlockSequenceOrder, Reference: fmt.Sprint(event.Sequence), Detail: "previous=" + fmt.Sprint(previous)}
		}
		previous = event.Sequence
		if !validReplayEntityKind(event.EntityKind) {
			return &ReplayBlock{Code: ReplayBlockEntityKind, Reference: string(event.EntityKind)}
		}
		if strings.TrimSpace(event.SourceID) == "" {
			return &ReplayBlock{Code: ReplayBlockIdentity, Reference: fmt.Sprint(event.Sequence)}
		}
		if event.LogicalVersion <= 0 {
			return &ReplayBlock{Code: ReplayBlockLogicalVersion, Reference: replayEventReference(event)}
		}
		switch event.Operation {
		case ReplayUpsert:
			if len(event.AfterImage) == 0 {
				return &ReplayBlock{Code: ReplayBlockMissingImage, Reference: replayEventReference(event), Detail: "upsert after-image"}
			}
		case ReplayDelete:
			if len(event.TombstoneImage) == 0 {
				return &ReplayBlock{Code: ReplayBlockMissingImage, Reference: replayEventReference(event), Detail: "delete tombstone"}
			}
		default:
			return &ReplayBlock{Code: ReplayBlockOperation, Reference: string(event.Operation)}
		}

	}
	return nil
}

func reduceReplayEvent(state *ReplayState, plan *ReplayPlan, event ReplayEvent) error {
	key := ReplayEntityKey{Kind: event.EntityKind, SourceID: event.SourceID}
	current, exists := state.Ledger[key]
	if exists && event.LogicalVersion < current.LogicalVersion {
		return nil
	}
	if exists && event.LogicalVersion == current.LogicalVersion {
		return reduceEqualVersion(state, key, current, event)
	}

	switch event.Operation {
	case ReplayUpsert:
		image := cloneReplayImage(event.AfterImage)
		state.Ledger[key] = ReplayLedgerEntry{LogicalVersion: event.LogicalVersion}
		state.Projection[key] = ReplayProjectionEntry{LogicalVersion: event.LogicalVersion, Image: image}
		plan.Steps = append(plan.Steps, ReplayStep{
			Sequence: event.Sequence, Entity: key, Operation: ReplayUpsert,
			LogicalVersion: event.LogicalVersion, Image: cloneReplayImage(image),
		})
	case ReplayDelete:
		state.Ledger[key] = ReplayLedgerEntry{LogicalVersion: event.LogicalVersion, Deleted: true}
		delete(state.Projection, key)
		plan.Steps = append(plan.Steps, ReplayStep{
			Sequence: event.Sequence, Entity: key, Operation: ReplayDelete,
			LogicalVersion: event.LogicalVersion, Image: cloneReplayImage(event.TombstoneImage),
		})
	}
	return nil
}

func reduceEqualVersion(state *ReplayState, key ReplayEntityKey, current ReplayLedgerEntry, event ReplayEvent) error {
	if current.Deleted {
		// A tombstone wins ties as well as older delayed source images. The
		// same delete is an ordinary idempotent retry.
		return nil
	}
	if event.Operation == ReplayDelete {
		return &ReplayBlock{Code: ReplayBlockVersionConflict, Reference: replayEventReference(event), Detail: "delete shares active version"}
	}
	projection, ok := state.Projection[key]
	if !ok || !reflect.DeepEqual(projection.Image, event.AfterImage) {
		return &ReplayBlock{Code: ReplayBlockVersionConflict, Reference: replayEventReference(event), Detail: "different after-image at same version"}
	}
	return nil
}

func validateReplayDependencyOrder(steps []ReplayStep) error {
	previous := -1
	for _, step := range steps {
		order := replayDependencyOrder(step.Entity.Kind, step.Operation)
		if order < previous {
			return &ReplayBlock{
				Code: ReplayBlockDependencyOrder, Reference: step.Entity.SourceID,
				Detail: fmt.Sprintf("kind=%s operation=%s", step.Entity.Kind, step.Operation),
			}
		}
		previous = order
	}
	return nil
}

func replayDependencyOrder(kind ReplayEntityKind, operation ReplayOperation) int {
	rank := map[ReplayEntityKind]int{
		ReplayEntityProject: 0, ReplayEntityTask: 1, ReplayEntityRule: 2,
		ReplayEntityOccurrence: 3, ReplayEntityEvent: 4,
	}[kind]
	if operation == ReplayDelete {
		return 5 + (4 - rank)
	}
	return rank
}

func validReplayEntityKind(kind ReplayEntityKind) bool {
	switch kind {
	case ReplayEntityProject, ReplayEntityTask, ReplayEntityRule, ReplayEntityOccurrence, ReplayEntityEvent:
		return true
	default:
		return false
	}
}

func cloneReplayState(state ReplayState) ReplayState {
	result := ReplayState{
		Watermark:  state.Watermark,
		Ledger:     make(map[ReplayEntityKey]ReplayLedgerEntry, len(state.Ledger)),
		Projection: make(map[ReplayEntityKey]ReplayProjectionEntry, len(state.Projection)),
	}
	for key, ledger := range state.Ledger {
		result.Ledger[key] = ledger
	}
	for key, projection := range state.Projection {
		projection.Image = cloneReplayImage(projection.Image)
		result.Projection[key] = projection
	}
	return result
}

func cloneReplayImage(image ReplayImage) ReplayImage {
	if image == nil {
		return nil
	}
	result := make(ReplayImage, len(image))
	for key, value := range image {
		result[key] = value
	}
	return result
}

func replayEventReference(event ReplayEvent) string {
	return fmt.Sprintf("%s/%s@%d", event.EntityKind, event.SourceID, event.Sequence)
}

func replayKeyReference(key ReplayEntityKey) string {
	return string(key.Kind) + "/" + key.SourceID
}
