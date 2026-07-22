package taskmigration

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
)

type ReconcileBlockCode string

const (
	ReconcileBlockInvalidIdentity   ReconcileBlockCode = "invalid_identity"
	ReconcileBlockMissingDigest     ReconcileBlockCode = "missing_digest"
	ReconcileBlockDuplicateIdentity ReconcileBlockCode = "duplicate_identity"
	ReconcileBlockMapConflict       ReconcileBlockCode = "id_map_conflict"
	ReconcileBlockDanglingMap       ReconcileBlockCode = "dangling_id_map"
)

type ReconcileBlock struct {
	Code      ReconcileBlockCode
	Reference string
	Detail    string
}

func (e *ReconcileBlock) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Reference)
	}
	return fmt.Sprintf("%s: %s: %s", e.Code, e.Reference, e.Detail)
}

// CanonicalSourceRow is one expected v2 projection row derived from the
// frozen legacy inventory. A single legacy source (notably Event) may produce
// more than one target row, but each target identity must be unique.
type CanonicalSourceRow struct {
	Source ReplayEntityKey
	Target ReplayEntityKey
	Digest string
}

type V2MappedRow struct {
	Target ReplayEntityKey
	Digest string
}

type ReconcileIDMap struct {
	Source ReplayEntityKey
	Target ReplayEntityKey
}

type ReconcileViolation struct {
	Entity ReplayEntityKey
	Detail string
}

type ReconcileInput struct {
	Source               []CanonicalSourceRow
	V2                   []V2MappedRow
	IDMaps               []ReconcileIDMap
	GeneratedSystemIDs   []ReplayEntityKey
	ForeignKeyViolations []ReconcileViolation
	StatusViolations     []ReconcileViolation
}

type ReconcileMismatchCode string

const (
	ReconcileMismatchRowCount   ReconcileMismatchCode = "row_count"
	ReconcileMismatchChecksum   ReconcileMismatchCode = "critical_checksum"
	ReconcileMismatchRowDigest  ReconcileMismatchCode = "row_digest"
	ReconcileMismatchForeignKey ReconcileMismatchCode = "foreign_key"
	ReconcileMismatchStatus     ReconcileMismatchCode = "status_invariant"
	ReconcileMismatchMissingMap ReconcileMismatchCode = "missing_id_map"
)

type ReconcileMismatch struct {
	Code         ReconcileMismatchCode
	Entity       ReplayEntityKey
	SourceDigest string
	V2Digest     string
	Detail       string
}

type ReconcileMutation struct {
	Source ReplayEntityKey
	Target ReplayEntityKey
	Digest string
}

type ReconcilePlan struct {
	UpsertMissing []ReconcileMutation
	DeleteExtra   []ReconcileMutation
	Mismatches    []ReconcileMismatch
	Ready         bool
}

type reconcilePair struct {
	Source ReplayEntityKey
	Target ReplayEntityKey
}

// ReconcileInventories compares one canonical legacy snapshot with the
// mapped v2 inventory. It performs no reads or writes. Provider code must
// apply the returned mutations and rerun reconciliation before cutover.
// Invalid or ambiguous identities return a zero plan.
func ReconcileInventories(input ReconcileInput) (ReconcilePlan, error) {
	sources, sourceByTarget, sourceIdentities, sourcePairs, err := indexCanonicalSources(input.Source)
	if err != nil {
		return ReconcilePlan{}, err
	}
	v2Rows, v2ByTarget, err := indexV2Rows(input.V2)
	if err != nil {
		return ReconcilePlan{}, err
	}
	generated, err := indexGeneratedIDs(input.GeneratedSystemIDs)
	if err != nil {
		return ReconcilePlan{}, err
	}
	maps, mapByTarget, err := indexReconcileMaps(input.IDMaps)
	if err != nil {
		return ReconcilePlan{}, err
	}
	if err := validateReconcileMaps(maps, sourceIdentities, sourcePairs, v2ByTarget, generated); err != nil {
		return ReconcilePlan{}, err
	}
	if err := validateReconcileViolations(input.ForeignKeyViolations); err != nil {
		return ReconcilePlan{}, err
	}
	if err := validateReconcileViolations(input.StatusViolations); err != nil {
		return ReconcilePlan{}, err
	}

	plan := ReconcilePlan{}
	for _, source := range sources {
		mapped, mappedOK := mapByTarget[source.Target]
		if !mappedOK || mapped.Source != source.Source {
			plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
				Code: ReconcileMismatchMissingMap, Entity: source.Target,
				Detail: "expected source=" + replayKeyReference(source.Source),
			})
		}
		v2, exists := v2ByTarget[source.Target]
		if !exists {
			plan.UpsertMissing = append(plan.UpsertMissing, ReconcileMutation{
				Source: source.Source, Target: source.Target, Digest: source.Digest,
			})
			continue
		}
		if source.Digest != v2.Digest {
			plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
				Code: ReconcileMismatchRowDigest, Entity: source.Target,
				SourceDigest: source.Digest, V2Digest: v2.Digest,
			})
		}
	}

	for _, v2 := range v2Rows {
		if _, protected := generated[v2.Target]; protected {
			continue
		}
		mapped, mappedOK := mapByTarget[v2.Target]
		if !mappedOK {
			// A v2-native row has no legacy provenance and is outside the
			// destructive side of this reconciliation.
			continue
		}
		if expected, exists := sourceByTarget[v2.Target]; exists && expected.Source == mapped.Source {
			continue
		}
		plan.DeleteExtra = append(plan.DeleteExtra, ReconcileMutation{
			Source: mapped.Source, Target: v2.Target, Digest: v2.Digest,
		})
	}

	// Aggregate checksums compare only targets which are still expected from
	// the frozen source. Generated system projects are not globally excluded:
	// a legacy personal/inbox project may legitimately map to one and must then
	// participate in the source/v2 checksum. Conversely, a protected generated
	// target whose legacy source was deleted remains outside the destructive
	// diff without making reconciliation permanently non-ready.
	mappedV2 := mappedExpectedV2Rows(v2Rows, sourceByTarget, mapByTarget)
	if len(sources) != len(mappedV2) {
		plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
			Code:   ReconcileMismatchRowCount,
			Detail: fmt.Sprintf("source=%d v2_mapped=%d", len(sources), len(mappedV2)),
		})
	}
	sourceChecksum := inventoryChecksumFromSources(sources)
	v2Checksum := inventoryChecksumFromV2(mappedV2)
	if sourceChecksum != v2Checksum {
		plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
			Code: ReconcileMismatchChecksum, SourceDigest: sourceChecksum, V2Digest: v2Checksum,
		})
	}
	for _, violation := range input.ForeignKeyViolations {
		plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
			Code: ReconcileMismatchForeignKey, Entity: violation.Entity, Detail: violation.Detail,
		})
	}
	for _, violation := range input.StatusViolations {
		plan.Mismatches = append(plan.Mismatches, ReconcileMismatch{
			Code: ReconcileMismatchStatus, Entity: violation.Entity, Detail: violation.Detail,
		})
	}

	sortReconcileMutations(plan.UpsertMissing, false)
	sortReconcileMutations(plan.DeleteExtra, true)
	sortReconcileMismatches(plan.Mismatches)
	plan.Ready = len(plan.UpsertMissing) == 0 && len(plan.DeleteExtra) == 0 && len(plan.Mismatches) == 0
	return plan, nil
}

func indexCanonicalSources(rows []CanonicalSourceRow) ([]CanonicalSourceRow, map[ReplayEntityKey]CanonicalSourceRow, map[ReplayEntityKey]struct{}, map[reconcilePair]struct{}, error) {
	result := append([]CanonicalSourceRow(nil), rows...)
	byTarget := make(map[ReplayEntityKey]CanonicalSourceRow, len(rows))
	sourceIDs := make(map[ReplayEntityKey]struct{}, len(rows))
	pairs := make(map[reconcilePair]struct{}, len(rows))
	for _, row := range rows {
		if err := validateReconcileKey(row.Source); err != nil {
			return nil, nil, nil, nil, err
		}
		if err := validateReconcileKey(row.Target); err != nil {
			return nil, nil, nil, nil, err
		}
		if strings.TrimSpace(row.Digest) == "" {
			return nil, nil, nil, nil, &ReconcileBlock{Code: ReconcileBlockMissingDigest, Reference: replayKeyReference(row.Target)}
		}
		if _, exists := byTarget[row.Target]; exists {
			return nil, nil, nil, nil, &ReconcileBlock{Code: ReconcileBlockDuplicateIdentity, Reference: replayKeyReference(row.Target), Detail: "canonical target"}
		}
		byTarget[row.Target] = row
		sourceIDs[row.Source] = struct{}{}
		pairs[reconcilePair{Source: row.Source, Target: row.Target}] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool { return lessReplayKey(result[i].Target, result[j].Target) })
	return result, byTarget, sourceIDs, pairs, nil
}

func indexV2Rows(rows []V2MappedRow) ([]V2MappedRow, map[ReplayEntityKey]V2MappedRow, error) {
	result := append([]V2MappedRow(nil), rows...)
	byTarget := make(map[ReplayEntityKey]V2MappedRow, len(rows))
	for _, row := range rows {
		if err := validateReconcileKey(row.Target); err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(row.Digest) == "" {
			return nil, nil, &ReconcileBlock{Code: ReconcileBlockMissingDigest, Reference: replayKeyReference(row.Target)}
		}
		if _, exists := byTarget[row.Target]; exists {
			return nil, nil, &ReconcileBlock{Code: ReconcileBlockDuplicateIdentity, Reference: replayKeyReference(row.Target), Detail: "v2 target"}
		}
		byTarget[row.Target] = row
	}
	sort.Slice(result, func(i, j int) bool { return lessReplayKey(result[i].Target, result[j].Target) })
	return result, byTarget, nil
}

func indexGeneratedIDs(ids []ReplayEntityKey) (map[ReplayEntityKey]struct{}, error) {
	result := make(map[ReplayEntityKey]struct{}, len(ids))
	for _, id := range ids {
		if err := validateReconcileKey(id); err != nil {
			return nil, err
		}
		if _, exists := result[id]; exists {
			return nil, &ReconcileBlock{Code: ReconcileBlockDuplicateIdentity, Reference: replayKeyReference(id), Detail: "generated system id"}
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func indexReconcileMaps(maps []ReconcileIDMap) ([]ReconcileIDMap, map[ReplayEntityKey]ReconcileIDMap, error) {
	result := append([]ReconcileIDMap(nil), maps...)
	byTarget := make(map[ReplayEntityKey]ReconcileIDMap, len(maps))
	for _, item := range maps {
		if err := validateReconcileKey(item.Source); err != nil {
			return nil, nil, err
		}
		if err := validateReconcileKey(item.Target); err != nil {
			return nil, nil, err
		}
		if current, exists := byTarget[item.Target]; exists {
			code := ReconcileBlockMapConflict
			detail := "already mapped from " + replayKeyReference(current.Source)
			if current.Source == item.Source {
				code = ReconcileBlockDuplicateIdentity
				detail = "duplicate id map"
			}
			return nil, nil, &ReconcileBlock{Code: code, Reference: replayKeyReference(item.Target), Detail: detail}
		}
		byTarget[item.Target] = item
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Target != result[j].Target {
			return lessReplayKey(result[i].Target, result[j].Target)
		}
		return lessReplayKey(result[i].Source, result[j].Source)
	})
	return result, byTarget, nil
}

func validateReconcileMaps(maps []ReconcileIDMap, sourceIDs map[ReplayEntityKey]struct{}, sourcePairs map[reconcilePair]struct{}, v2 map[ReplayEntityKey]V2MappedRow, generated map[ReplayEntityKey]struct{}) error {
	for _, item := range maps {
		if _, protected := generated[item.Target]; protected {
			continue
		}
		_, sourceExists := sourceIDs[item.Source]
		_, pairExpected := sourcePairs[reconcilePair{Source: item.Source, Target: item.Target}]
		_, targetExists := v2[item.Target]
		if !targetExists && (!sourceExists || !pairExpected) {
			return &ReconcileBlock{
				Code: ReconcileBlockDanglingMap, Reference: replayKeyReference(item.Target),
				Detail: "source=" + replayKeyReference(item.Source),
			}
		}
	}
	return nil
}

func validateReconcileViolations(violations []ReconcileViolation) error {
	for _, violation := range violations {
		if err := validateReconcileKey(violation.Entity); err != nil {
			return err
		}
	}
	return nil
}

func validateReconcileKey(key ReplayEntityKey) error {
	if !validReconcileEntityKind(key.Kind) || strings.TrimSpace(key.SourceID) == "" {
		return &ReconcileBlock{Code: ReconcileBlockInvalidIdentity, Reference: replayKeyReference(key)}
	}
	return nil
}

func validReconcileEntityKind(kind ReplayEntityKind) bool {
	return validReplayEntityKind(kind) || kind == ReplayEntitySchedule || kind == ReplayEntityRoadmap || kind == ReplayEntityRoadmapNode || kind == ReplayEntityRoadmapEdge
}

func mappedExpectedV2Rows(
	rows []V2MappedRow,
	expected map[ReplayEntityKey]CanonicalSourceRow,
	maps map[ReplayEntityKey]ReconcileIDMap,
) []V2MappedRow {
	result := make([]V2MappedRow, 0, len(rows))
	for _, row := range rows {
		source, expectedTarget := expected[row.Target]
		mapped, mappedTarget := maps[row.Target]
		if !expectedTarget || !mappedTarget || source.Source != mapped.Source {
			continue
		}
		result = append(result, row)
	}
	return result
}

func inventoryChecksumFromSources(rows []CanonicalSourceRow) string {
	parts := make([]string, len(rows))
	for i, row := range rows {
		parts[i] = replayKeyReference(row.Target) + "\x00" + row.Digest
	}
	sort.Strings(parts)
	return checksumParts(parts)
}

func inventoryChecksumFromV2(rows []V2MappedRow) string {
	parts := make([]string, len(rows))
	for i, row := range rows {
		parts[i] = replayKeyReference(row.Target) + "\x00" + row.Digest
	}
	sort.Strings(parts)
	return checksumParts(parts)
}

func checksumParts(parts []string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func sortReconcileMutations(mutations []ReconcileMutation, deleting bool) {
	sort.Slice(mutations, func(i, j int) bool {
		leftRank := reconcileKindRank(mutations[i].Target.Kind)
		rightRank := reconcileKindRank(mutations[j].Target.Kind)
		if leftRank != rightRank {
			if deleting {
				return leftRank > rightRank
			}
			return leftRank < rightRank
		}
		if mutations[i].Target != mutations[j].Target {
			return lessReplayKey(mutations[i].Target, mutations[j].Target)
		}
		return lessReplayKey(mutations[i].Source, mutations[j].Source)
	})
}

func sortReconcileMismatches(mismatches []ReconcileMismatch) {
	sort.Slice(mismatches, func(i, j int) bool {
		if mismatches[i].Code != mismatches[j].Code {
			return mismatches[i].Code < mismatches[j].Code
		}
		if mismatches[i].Entity != mismatches[j].Entity {
			return lessReplayKey(mismatches[i].Entity, mismatches[j].Entity)
		}
		if mismatches[i].SourceDigest != mismatches[j].SourceDigest {
			return mismatches[i].SourceDigest < mismatches[j].SourceDigest
		}
		if mismatches[i].V2Digest != mismatches[j].V2Digest {
			return mismatches[i].V2Digest < mismatches[j].V2Digest
		}
		return mismatches[i].Detail < mismatches[j].Detail
	})
}

func reconcileKindRank(kind ReplayEntityKind) int {
	switch kind {
	case ReplayEntityProject:
		return 0
	case ReplayEntityRoadmap:
		return 1
	case ReplayEntityRoadmapNode:
		return 2
	case ReplayEntityRoadmapEdge:
		return 3
	case ReplayEntityTask:
		return 4
	case ReplayEntitySchedule, ReplayEntityRule:
		return 5
	case ReplayEntityOccurrence:
		return 6
	case ReplayEntityEvent:
		return 7
	default:
		return 8
	}
}

func lessReplayKey(left, right ReplayEntityKey) bool {
	if left.Kind != right.Kind {
		return left.Kind < right.Kind
	}
	return left.SourceID < right.SourceID
}

type DrainPreconditions struct {
	OutboxWatermark          int64
	CutoverSequence          int64
	ActiveLegacyTransactions int
	OldWriterHeartbeats      int
	AcceptLegacyWrites       bool
	PreviousFenceEpoch       int64
	CurrentFenceEpoch        int64
}

type DrainFailureCode string

const (
	DrainFailureOutboxLag             DrainFailureCode = "outbox_watermark_not_caught_up"
	DrainFailureActiveTransactions    DrainFailureCode = "active_legacy_transactions"
	DrainFailureOldWriterHeartbeat    DrainFailureCode = "old_writer_heartbeat"
	DrainFailureLegacyWritesEnabled   DrainFailureCode = "legacy_writes_enabled"
	DrainFailureFenceEpochNotAdvanced DrainFailureCode = "fence_epoch_not_advanced"
)

type DrainFailure struct {
	Code   DrainFailureCode
	Detail string
}

type DrainPreconditionResult struct {
	Ready    bool
	Failures []DrainFailure
}

// EvaluateDrainPreconditions is a pure observation check. Database locking,
// heartbeat reads, trigger enforcement, and epoch mutation are provider and
// runtime responsibilities.
func EvaluateDrainPreconditions(input DrainPreconditions) DrainPreconditionResult {
	result := DrainPreconditionResult{}
	if input.OutboxWatermark < 0 || input.CutoverSequence < 0 || input.OutboxWatermark < input.CutoverSequence {
		result.Failures = append(result.Failures, DrainFailure{
			Code:   DrainFailureOutboxLag,
			Detail: fmt.Sprintf("watermark=%d cutover_sequence=%d", input.OutboxWatermark, input.CutoverSequence),
		})
	}
	if input.ActiveLegacyTransactions != 0 {
		result.Failures = append(result.Failures, DrainFailure{
			Code: DrainFailureActiveTransactions, Detail: fmt.Sprintf("count=%d", input.ActiveLegacyTransactions),
		})
	}
	if input.OldWriterHeartbeats != 0 {
		result.Failures = append(result.Failures, DrainFailure{
			Code: DrainFailureOldWriterHeartbeat, Detail: fmt.Sprintf("count=%d", input.OldWriterHeartbeats),
		})
	}
	if input.AcceptLegacyWrites {
		result.Failures = append(result.Failures, DrainFailure{Code: DrainFailureLegacyWritesEnabled})
	}
	if input.PreviousFenceEpoch < 0 || input.CurrentFenceEpoch <= input.PreviousFenceEpoch {
		result.Failures = append(result.Failures, DrainFailure{
			Code:   DrainFailureFenceEpochNotAdvanced,
			Detail: fmt.Sprintf("previous=%d current=%d", input.PreviousFenceEpoch, input.CurrentFenceEpoch),
		})
	}
	result.Ready = len(result.Failures) == 0
	return result
}
