package taskdomain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const ErrorCodeOccurrenceReopenRequired ErrorCode = "occurrence_reopen_required"

var (
	ErrInvalidScheduleCommand   = errors.New("invalid schedule command")
	ErrOccurrenceReopenRequired = &domainError{code: ErrorCodeOccurrenceReopenRequired}
)

// ScheduleCommandFencer is intentionally separate from the production tenant
// adapter until providers implement the atomic schedule-change writer below.
// Its callback is invoked only after the workspace epoch is validated and
// locked.
type ScheduleCommandFencer interface {
	BeginFencedScheduleWrite(context.Context, string, int64, func(ScheduleCommandFencedTx) error) error
}

type ScheduleCommandFencedTx interface {
	ScheduleCommandWriter() ScheduleCommandWriter
}

// ScheduleCommandWriter makes the two schedule mutations indivisible. In
// particular, installing a version and reconciling all affected occurrences
// is one provider operation, never InstallScheduleVersion followed by a
// second best-effort write.
type ScheduleCommandWriter interface {
	ApplyOccurrenceReschedule(context.Context, OccurrenceRescheduleWrite) error
	ApplyScheduleVersionChange(context.Context, ScheduleVersionChangeWrite) error
}

type ScheduleCommandStateReader interface {
	GetScheduleCommandState(context.Context, string) (ScheduleCommandState, error)
}

type ScheduleCommandState struct {
	WorkspaceID  string
	TaskID       string
	TaskRevision int64
	Schedule     ScheduleHeader
	Versions     []ScheduleVersion
	Occurrences  []ScheduleOccurrenceSnapshot
}

type ScheduleOccurrenceSnapshot struct {
	Record             OccurrenceRecord
	ActualStartAt      *time.Time
	CompletedAt        *time.Time
	ManuallyOverridden bool
}

type OccurrenceTimingInput struct {
	TimingType            TimingType
	Timezone              string
	PlannedDate           string
	AllDayEndDate         string
	LocalStartTime        string
	DurationMinutes       int
	SelectedOffsetSeconds *int
}

type RescheduleOccurrenceRequest struct {
	WorkspaceID                string
	TaskID                     string
	OccurrenceID               string
	ExpectedRuntimeEpoch       int64
	ExpectedTaskRevision       int64
	ExpectedScheduleRevision   int64
	ExpectedOccurrenceRevision int64
	Timing                     OccurrenceTimingInput
}

type RescheduleThisAndFutureRequest struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedRuntimeEpoch     int64
	ExpectedTaskRevision     int64
	ExpectedScheduleRevision int64
	EffectiveFrom            string
	GenerateThroughExclusive string
	Schedule                 ScheduleInput
	// SelectedOffsets resolves fall-back overlaps by local occurrence date.
	SelectedOffsets map[string]int
}

type OccurrenceRescheduleWrite struct {
	WorkspaceID                string
	TaskID                     string
	ExpectedTaskRevision       int64
	ExpectedScheduleRevision   int64
	ExpectedOccurrenceRevision int64
	After                      ScheduleOccurrenceSnapshot
}

type ScheduleVersionChangeWrite struct {
	WorkspaceID                 string
	TaskID                      string
	ExpectedTaskRevision        int64
	ExpectedScheduleRevision    int64
	Schedule                    ScheduleHeader
	ClosedVersion               ScheduleVersion
	NewVersion                  ScheduleVersion
	PreservedOccurrenceIDs      []string
	UpsertOccurrences           []ScheduleOccurrenceSnapshot
	ExpectedOccurrenceRevisions map[string]int64
	DeleteOccurrenceRevisions   map[string]int64
}

type ScheduleCommandResult struct {
	taskRevision       int64
	scheduleRevision   int64
	occurrenceRevision int64
	scheduleVersion    int64
	candidates         []OffsetCandidate
}

func (result ScheduleCommandResult) TaskRevision() int64       { return result.taskRevision }
func (result ScheduleCommandResult) ScheduleRevision() int64   { return result.scheduleRevision }
func (result ScheduleCommandResult) OccurrenceRevision() int64 { return result.occurrenceRevision }
func (result ScheduleCommandResult) ScheduleVersion() int64    { return result.scheduleVersion }
func (result ScheduleCommandResult) Candidates() []OffsetCandidate {
	return append([]OffsetCandidate(nil), result.candidates...)
}
func (result ScheduleCommandResult) IsZero() bool {
	return result.taskRevision == 0 && result.scheduleRevision == 0 && result.occurrenceRevision == 0 &&
		result.scheduleVersion == 0 && len(result.candidates) == 0
}

type ScheduleService struct {
	fencer ScheduleCommandFencer
	reader ScheduleCommandStateReader
}

func NewScheduleService(fencer ScheduleCommandFencer, reader ScheduleCommandStateReader) *ScheduleService {
	return &ScheduleService{fencer: fencer, reader: reader}
}

func (service *ScheduleService) RescheduleOccurrence(ctx context.Context, request RescheduleOccurrenceRequest) (ScheduleCommandResult, error) {
	if service == nil || service.fencer == nil || service.reader == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.OccurrenceID) == "" || request.ExpectedRuntimeEpoch < 1 ||
		request.ExpectedTaskRevision < 1 || request.ExpectedScheduleRevision < 1 || request.ExpectedOccurrenceRevision < 1 {
		return ScheduleCommandResult{}, ErrInvalidScheduleCommand
	}

	var result ScheduleCommandResult
	err := service.fencer.BeginFencedScheduleWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx ScheduleCommandFencedTx) error {
		if tx == nil || tx.ScheduleCommandWriter() == nil {
			return ErrInvalidScheduleCommand
		}
		state, err := service.reader.GetScheduleCommandState(ctx, request.TaskID)
		if err != nil {
			return err
		}
		if err := validateScheduleCommandStateIdentity(state, request.WorkspaceID, request.TaskID); err != nil {
			return err
		}
		if state.TaskRevision != request.ExpectedTaskRevision {
			return ErrTaskRevisionConflict
		}
		if state.Schedule.Revision != request.ExpectedScheduleRevision {
			return ErrScheduleRevisionConflict
		}
		current, found := findScheduleOccurrence(state.Occurrences, request.OccurrenceID)
		if !found {
			return ErrOccurrenceNotFound
		}
		if current.Record.Revision != request.ExpectedOccurrenceRevision {
			return ErrOccurrenceRevisionConflict
		}
		if isTerminalExecutionStatus(current.Record.ExecutionStatus) || current.CompletedAt != nil {
			return ErrOccurrenceReopenRequired
		}

		after, candidates, err := rescheduleOccurrenceAfterImage(current, request.Timing)
		if err != nil {
			result.candidates = append([]OffsetCandidate(nil), candidates...)
			return err
		}
		write := OccurrenceRescheduleWrite{
			WorkspaceID: request.WorkspaceID, TaskID: request.TaskID,
			ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
			ExpectedOccurrenceRevision: request.ExpectedOccurrenceRevision, After: after,
		}
		if err := tx.ScheduleCommandWriter().ApplyOccurrenceReschedule(ctx, write); err != nil {
			return err
		}
		result = ScheduleCommandResult{
			taskRevision: state.TaskRevision, scheduleRevision: state.Schedule.Revision,
			occurrenceRevision: after.Record.Revision, scheduleVersion: after.Record.GeneratedScheduleRevision,
			candidates: append([]OffsetCandidate(nil), candidates...),
		}
		return nil
	})
	if err != nil {
		if len(result.candidates) > 0 && (ErrorCodeOf(err) == ErrorCodeAmbiguousLocalTime || ErrorCodeOf(err) == ErrorCodeNonexistentLocalTime) {
			return result, err
		}
		return ScheduleCommandResult{}, err
	}
	return result, nil
}

func (service *ScheduleService) RescheduleThisAndFuture(ctx context.Context, request RescheduleThisAndFutureRequest) (ScheduleCommandResult, error) {
	if service == nil || service.fencer == nil || service.reader == nil || strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.TaskID) == "" || request.ExpectedRuntimeEpoch < 1 || request.ExpectedTaskRevision < 1 ||
		request.ExpectedScheduleRevision < 1 {
		return ScheduleCommandResult{}, ErrInvalidScheduleCommand
	}
	normalized, err := NormalizeSchedule(request.Schedule)
	if err != nil {
		return ScheduleCommandResult{}, err
	}
	if _, err := requiredGeneratorDate(request.EffectiveFrom, "effective from"); err != nil {
		return ScheduleCommandResult{}, err
	}
	if _, err := CalculateOccurrenceKeys(normalized,
		ScheduleEffectiveRange{From: request.EffectiveFrom},
		OccurrenceWindow{From: request.EffectiveFrom, To: request.GenerateThroughExclusive}); err != nil {
		return ScheduleCommandResult{}, err
	}

	var result ScheduleCommandResult
	err = service.fencer.BeginFencedScheduleWrite(ctx, request.WorkspaceID, request.ExpectedRuntimeEpoch, func(tx ScheduleCommandFencedTx) error {
		if tx == nil || tx.ScheduleCommandWriter() == nil {
			return ErrInvalidScheduleCommand
		}
		state, err := service.reader.GetScheduleCommandState(ctx, request.TaskID)
		if err != nil {
			return err
		}
		if err := validateScheduleCommandStateIdentity(state, request.WorkspaceID, request.TaskID); err != nil {
			return err
		}
		if state.TaskRevision != request.ExpectedTaskRevision {
			return ErrTaskRevisionConflict
		}
		if state.Schedule.Revision != request.ExpectedScheduleRevision {
			return ErrScheduleRevisionConflict
		}
		current, found := currentScheduleVersion(state)
		if !found {
			return ErrInvalidScheduleCommand
		}
		newScheduleRevision := nextScheduleVersion(state.Versions)
		newVersion, err := persistenceScheduleVersion(request.WorkspaceID, request.TaskID, newScheduleRevision, request.EffectiveFrom, normalized)
		if err != nil {
			return err
		}
		closed := current
		closed.EffectiveTo = request.EffectiveFrom
		write, candidates, err := reconcileScheduleOccurrences(state, request, normalized, closed, newVersion)
		if err != nil {
			result.candidates = append([]OffsetCandidate(nil), candidates...)
			return err
		}
		if err := tx.ScheduleCommandWriter().ApplyScheduleVersionChange(ctx, write); err != nil {
			return err
		}
		result = ScheduleCommandResult{
			taskRevision: state.TaskRevision, scheduleRevision: write.Schedule.Revision,
			scheduleVersion: newScheduleRevision, candidates: append([]OffsetCandidate(nil), candidates...),
		}
		return nil
	})
	if err != nil {
		if ErrorCodeOf(err) == ErrorCodeAmbiguousLocalTime || ErrorCodeOf(err) == ErrorCodeNonexistentLocalTime {
			return result, err
		}
		return ScheduleCommandResult{}, err
	}
	return result, nil
}

func rescheduleOccurrenceAfterImage(current ScheduleOccurrenceSnapshot, input OccurrenceTimingInput) (ScheduleOccurrenceSnapshot, []OffsetCandidate, error) {
	normalized, err := NormalizeSchedule(ScheduleInput{
		RecurrenceType: RecurrenceNone, TimingType: input.TimingType, Timezone: input.Timezone,
		StartsOn: input.PlannedDate, LocalStartTime: input.LocalStartTime, DurationMinutes: input.DurationMinutes,
	})
	if err != nil {
		return ScheduleOccurrenceSnapshot{}, nil, err
	}
	after := current
	after.Record.PlannedDate = ""
	after.Record.PlannedStartAt = nil
	after.Record.PlannedEndAt = nil
	after.Record.AllDayEndDate = ""
	after.Record.Revision++
	after.ManuallyOverridden = true

	switch normalized.TimingType {
	case TimingUnscheduled:
		return after, nil, nil
	case TimingDate:
		if _, err := ResolveAllDayRangeUTC(normalized.StartsOn, input.AllDayEndDate, normalized.Timezone); err != nil {
			return ScheduleOccurrenceSnapshot{}, nil, err
		}
		after.Record.PlannedDate = normalized.StartsOn
		after.Record.AllDayEndDate = input.AllDayEndDate
		return after, nil, nil
	case TimingTimeBlock:
		instantRange, candidates, err := ResolveTimeBlockUTC(normalized.StartsOn, normalized.LocalStartTime, normalized.Timezone, normalized.DurationMinutes, input.SelectedOffsetSeconds)
		if err != nil {
			return ScheduleOccurrenceSnapshot{}, candidates, err
		}
		after.Record.PlannedDate = normalized.StartsOn
		after.Record.PlannedStartAt = cloneScheduleServiceTime(&instantRange.StartUTC)
		after.Record.PlannedEndAt = cloneScheduleServiceTime(&instantRange.EndUTC)
		return after, candidates, nil
	default:
		return ScheduleOccurrenceSnapshot{}, nil, ErrInvalidScheduleCommand
	}
}

func reconcileScheduleOccurrences(
	state ScheduleCommandState,
	request RescheduleThisAndFutureRequest,
	normalized Schedule,
	closedVersion ScheduleVersion,
	newVersion ScheduleVersion,
) (ScheduleVersionChangeWrite, []OffsetCandidate, error) {
	keys, err := CalculateOccurrenceKeys(normalized,
		ScheduleEffectiveRange{From: request.EffectiveFrom},
		OccurrenceWindow{From: request.EffectiveFrom, To: request.GenerateThroughExclusive})
	if err != nil {
		return ScheduleVersionChangeWrite{}, nil, err
	}
	desired := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		desired[key] = struct{}{}
	}
	write := ScheduleVersionChangeWrite{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID,
		ExpectedTaskRevision: request.ExpectedTaskRevision, ExpectedScheduleRevision: request.ExpectedScheduleRevision,
		Schedule: ScheduleHeader{
			WorkspaceID: request.WorkspaceID, TaskID: request.TaskID,
			Revision: state.Schedule.Revision + 1, CurrentScheduleRevision: newVersion.ScheduleRevision,
		},
		ClosedVersion: closedVersion, NewVersion: newVersion,
		ExpectedOccurrenceRevisions: make(map[string]int64),
		DeleteOccurrenceRevisions:   make(map[string]int64),
	}
	allCandidates := make([]OffsetCandidate, 0)
	for _, existing := range state.Occurrences {
		if scheduleOccurrenceMustBePreserved(existing, request.EffectiveFrom) {
			write.PreservedOccurrenceIDs = append(write.PreservedOccurrenceIDs, existing.Record.ID)
			delete(desired, existing.Record.OccurrenceKey)
			continue
		}
		if _, keep := desired[existing.Record.OccurrenceKey]; !keep {
			write.ExpectedOccurrenceRevisions[existing.Record.ID] = existing.Record.Revision
			write.DeleteOccurrenceRevisions[existing.Record.ID] = existing.Record.Revision
			continue
		}
		generated, candidates, err := materializeScheduleOccurrence(request, normalized, newVersion.ScheduleRevision, existing.Record.OccurrenceKey)
		if err != nil {
			return ScheduleVersionChangeWrite{}, candidates, err
		}
		generated.Record.ID = existing.Record.ID
		generated.Record.NoteID = existing.Record.NoteID
		generated.Record.DueAt = cloneScheduleServiceTime(existing.Record.DueAt)
		generated.Record.Revision = existing.Record.Revision + 1
		write.ExpectedOccurrenceRevisions[existing.Record.ID] = existing.Record.Revision
		write.UpsertOccurrences = append(write.UpsertOccurrences, generated)
		allCandidates = append(allCandidates, candidates...)
		delete(desired, existing.Record.OccurrenceKey)
	}
	remaining := make([]string, 0, len(desired))
	for key := range desired {
		remaining = append(remaining, key)
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		generated, candidates, err := materializeScheduleOccurrence(request, normalized, newVersion.ScheduleRevision, key)
		if err != nil {
			return ScheduleVersionChangeWrite{}, candidates, err
		}
		generated.Record.ID = fmt.Sprintf("%s:schedule:%d:%s", request.TaskID, newVersion.ScheduleRevision, key)
		write.UpsertOccurrences = append(write.UpsertOccurrences, generated)
		allCandidates = append(allCandidates, candidates...)
	}
	sort.Strings(write.PreservedOccurrenceIDs)
	sort.SliceStable(write.UpsertOccurrences, func(left, right int) bool {
		return write.UpsertOccurrences[left].Record.OccurrenceKey < write.UpsertOccurrences[right].Record.OccurrenceKey
	})
	return write, allCandidates, nil
}

func materializeScheduleOccurrence(
	request RescheduleThisAndFutureRequest,
	normalized Schedule,
	scheduleRevision int64,
	key string,
) (ScheduleOccurrenceSnapshot, []OffsetCandidate, error) {
	localDate := key
	if normalized.RecurrenceType == RecurrenceNone {
		localDate = normalized.StartsOn
	}
	record := OccurrenceRecord{
		WorkspaceID: request.WorkspaceID, TaskID: request.TaskID, OccurrenceKey: key,
		ExecutionStatus: ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: scheduleRevision,
	}
	switch normalized.TimingType {
	case TimingDate:
		record.PlannedDate = localDate
	case TimingTimeBlock:
		var selected *int
		if offset, exists := request.SelectedOffsets[localDate]; exists {
			selected = &offset
		} else if offset, exists := request.SelectedOffsets[key]; exists {
			selected = &offset
		}
		instantRange, candidates, err := ResolveTimeBlockUTC(localDate, normalized.LocalStartTime, normalized.Timezone, normalized.DurationMinutes, selected)
		if err != nil {
			return ScheduleOccurrenceSnapshot{}, candidates, err
		}
		record.PlannedDate = localDate
		record.PlannedStartAt = cloneScheduleServiceTime(&instantRange.StartUTC)
		record.PlannedEndAt = cloneScheduleServiceTime(&instantRange.EndUTC)
		return ScheduleOccurrenceSnapshot{Record: record}, candidates, nil
	case TimingUnscheduled:
		// An unscheduled version intentionally has no generated future keys.
	default:
		return ScheduleOccurrenceSnapshot{}, nil, ErrInvalidScheduleCommand
	}
	return ScheduleOccurrenceSnapshot{Record: record}, nil, nil
}

func persistenceScheduleVersion(workspaceID, taskID string, revision int64, effectiveFrom string, schedule Schedule) (ScheduleVersion, error) {
	rule := `{}`
	if schedule.Rule != nil {
		encoded, err := json.Marshal(schedule.Rule)
		if err != nil {
			return ScheduleVersion{}, err
		}
		rule = string(encoded)
	}
	version := ScheduleVersion{
		WorkspaceID: workspaceID, TaskID: taskID, ScheduleRevision: revision, EffectiveFrom: effectiveFrom,
		RecurrenceType: schedule.RecurrenceType, TimingType: schedule.TimingType, Timezone: schedule.Timezone,
		StartsOn: schedule.StartsOn, EndsOn: schedule.EndsOn, RecurrenceRule: rule,
		LocalStartTime: schedule.LocalStartTime, DurationMinutes: schedule.DurationMinutes,
	}
	if err := ValidateScheduleVersionInstall(ScheduleVersionInstall{
		WorkspaceID: workspaceID, TaskID: taskID, ExpectedScheduleRevision: 1, Version: version,
	}); err != nil {
		return ScheduleVersion{}, err
	}
	return version, nil
}

func validateScheduleCommandStateIdentity(state ScheduleCommandState, workspaceID, taskID string) error {
	if state.WorkspaceID != workspaceID || state.TaskID != taskID || state.Schedule.WorkspaceID != workspaceID || state.Schedule.TaskID != taskID {
		return ErrInvalidScheduleCommand
	}
	return nil
}

func findScheduleOccurrence(occurrences []ScheduleOccurrenceSnapshot, occurrenceID string) (ScheduleOccurrenceSnapshot, bool) {
	for _, occurrence := range occurrences {
		if occurrence.Record.ID == occurrenceID {
			return occurrence, true
		}
	}
	return ScheduleOccurrenceSnapshot{}, false
}

func currentScheduleVersion(state ScheduleCommandState) (ScheduleVersion, bool) {
	for _, version := range state.Versions {
		if version.ScheduleRevision == state.Schedule.CurrentScheduleRevision && version.EffectiveTo == "" {
			return version, true
		}
	}
	return ScheduleVersion{}, false
}

func nextScheduleVersion(versions []ScheduleVersion) int64 {
	var maximum int64
	for _, version := range versions {
		if version.ScheduleRevision > maximum {
			maximum = version.ScheduleRevision
		}
	}
	return maximum + 1
}

func scheduleOccurrenceMustBePreserved(occurrence ScheduleOccurrenceSnapshot, effectiveFrom string) bool {
	if occurrence.Record.PlannedDate == "" || occurrence.Record.PlannedDate < effectiveFrom {
		return true
	}
	return occurrence.ManuallyOverridden || occurrence.ActualStartAt != nil || occurrence.CompletedAt != nil ||
		occurrence.Record.ExecutionStatus == ExecutionStatusActive || occurrence.Record.ExecutionStatus == ExecutionStatusBlocked ||
		isTerminalExecutionStatus(occurrence.Record.ExecutionStatus)
}

func cloneScheduleServiceTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
