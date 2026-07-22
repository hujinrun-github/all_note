package taskdomain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidGenerationWorker = errors.New("invalid generation worker request")
	ErrGenerationClaimAck      = errors.New("generation claim acknowledgement failed")
)

const GenerationClaimRetryDelay = time.Minute

type GenerationClaimErrorCode string

const (
	GenerationClaimErrorNone           GenerationClaimErrorCode = ""
	GenerationClaimErrorInvalidRuntime GenerationClaimErrorCode = "invalid_runtime"
	GenerationClaimErrorRuntimeResolve GenerationClaimErrorCode = "runtime_resolve_failed"
	GenerationClaimErrorFencedWrite    GenerationClaimErrorCode = "fenced_write_failed"
)

type GenerationWorkspaceClaim struct {
	ClaimID      string
	WorkspaceID  string
	CreatedEpoch int64
}

// GenerationClaimOutcome is the immutable value acknowledged to the claim
// store. It deliberately contains only normalized error codes: raw dependency
// errors can contain credentials and must never enter durable claim state.
// CreatedEpoch is the resource snapshot audit fact while RuntimeEpoch is the
// fresh epoch used by this execution attempt.
type GenerationClaimOutcome struct {
	ClaimID             string                   `json:"claim_id"`
	WorkspaceID         string                   `json:"workspace_id"`
	CreatedEpoch        int64                    `json:"created_epoch"`
	RuntimeEpoch        int64                    `json:"runtime_epoch"`
	Status              GenerationStatus         `json:"status"`
	Inserted            int                      `json:"inserted"`
	GenerationWatermark string                   `json:"generation_watermark,omitempty"`
	RetryAt             time.Time                `json:"retry_at,omitempty"`
	ErrorCode           GenerationClaimErrorCode `json:"error_code,omitempty"`
}

// WorkspaceClaimSource owns both sides of the lease protocol. Completion must
// be idempotent by ClaimID because a successful tenant write can be followed by
// an acknowledgement failure and a later lease retry.
type WorkspaceClaimSource interface {
	ClaimGenerationWorkspaces(context.Context, int, time.Time) ([]GenerationWorkspaceClaim, error)
	CompleteGenerationClaim(context.Context, GenerationClaimOutcome) error
}

type GenerationScheduleVersion struct {
	Revision  int64
	Schedule  Schedule
	Effective ScheduleEffectiveRange
}

type GenerationTargetState struct {
	TaskID                 string
	ScheduleRevision       int64
	GenerationWatermark    string
	GenerationEnabled      bool
	Versions               []GenerationScheduleVersion
	ExistingOccurrenceKeys []string
}

type GenerationStateReader interface {
	ListGenerationTargets(context.Context) ([]GenerationTargetState, error)
}

type GenerationOccurrence struct {
	WorkspaceID               string
	TaskID                    string
	ID                        string
	OccurrenceKey             string
	PlannedDate               string
	PlannedStartAt            *time.Time
	PlannedEndAt              *time.Time
	GeneratedScheduleRevision int64
}

type GenerationInsert struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedScheduleRevision int64
	Occurrences              []GenerationOccurrence
}

type GenerationCompletion struct {
	WorkspaceID              string
	TaskID                   string
	ExpectedScheduleRevision int64
	GenerationWatermark      string
	Status                   GenerationStatus
	Error                    string
	RetryAt                  *time.Time
	RetryPendingJobs         int
	FailedJobs               int
}

type GenerationWriter interface {
	InsertMissingOccurrences(context.Context, GenerationInsert) error
	CompleteGeneration(context.Context, GenerationCompletion) error
}

// GenerationFencer must roll back all writer calls when callback returns an
// error. Both the state read and writes therefore share one epoch-validated
// transaction.
type GenerationFencer interface {
	BeginGenerationWrite(context.Context, string, int64, func(GenerationStateReader, GenerationWriter) error) error
}

type GenerationRuntimeSnapshot struct {
	WorkspaceID string
	Epoch       int64
	Fencer      GenerationFencer
}

type GenerationRuntimeResolver interface {
	ResolveGenerationRuntime(context.Context, string) (GenerationRuntimeSnapshot, error)
}

type GenerationBatchRequest struct {
	Limit int
	Now   time.Time
}

type GenerationWorkspaceResult struct {
	GenerationClaimOutcome
	Acknowledged bool
	Error        error
	AckError     error
}

type GenerationWorker struct {
	claims   WorkspaceClaimSource
	runtimes GenerationRuntimeResolver
}

func NewGenerationWorker(claims WorkspaceClaimSource, runtimes GenerationRuntimeResolver) *GenerationWorker {
	return &GenerationWorker{claims: claims, runtimes: runtimes}
}

func (worker *GenerationWorker) RunBatch(ctx context.Context, request GenerationBatchRequest) ([]GenerationWorkspaceResult, error) {
	if worker == nil || worker.claims == nil || worker.runtimes == nil || request.Limit < 1 || request.Now.IsZero() {
		return nil, ErrInvalidGenerationWorker
	}
	claims, err := worker.claims.ClaimGenerationWorkspaces(ctx, request.Limit, request.Now)
	if err != nil {
		return nil, err
	}

	results := make([]GenerationWorkspaceResult, 0, len(claims))
	for _, claim := range claims {
		result := GenerationWorkspaceResult{
			GenerationClaimOutcome: GenerationClaimOutcome{
				ClaimID: claim.ClaimID, WorkspaceID: claim.WorkspaceID, CreatedEpoch: claim.CreatedEpoch,
			},
		}
		if strings.TrimSpace(claim.ClaimID) == "" || strings.TrimSpace(claim.WorkspaceID) == "" || claim.CreatedEpoch < 1 {
			result.Status = GenerationStatusFailed
			result.Error = ErrInvalidGenerationWorker
			results = append(results, result)
			continue
		}

		runtime, resolveErr := worker.runtimes.ResolveGenerationRuntime(ctx, claim.WorkspaceID)
		if resolveErr != nil {
			result.Status = generationFailureStatus(resolveErr)
			result.ErrorCode = GenerationClaimErrorRuntimeResolve
			result.RetryAt = generationClaimRetryAt(result.Status, request.Now)
			result.Error = resolveErr
			results = append(results, worker.acknowledge(ctx, result))
			continue
		}
		result.RuntimeEpoch = runtime.Epoch
		if runtime.WorkspaceID != claim.WorkspaceID || runtime.Epoch < 1 || runtime.Fencer == nil {
			result.Status = GenerationStatusFailed
			result.ErrorCode = GenerationClaimErrorInvalidRuntime
			result.Error = ErrInvalidGenerationWorker
			results = append(results, worker.acknowledge(ctx, result))
			continue
		}

		inserted := 0
		watermark := ""
		writeErr := runtime.Fencer.BeginGenerationWrite(ctx, claim.WorkspaceID, runtime.Epoch, func(reader GenerationStateReader, writer GenerationWriter) error {
			if reader == nil || writer == nil {
				return ErrInvalidGenerationWorker
			}
			targets, readErr := reader.ListGenerationTargets(ctx)
			if readErr != nil {
				return readErr
			}
			for _, target := range targets {
				if !target.GenerationEnabled {
					continue
				}
				occurrences, targetWatermark, planErr := planGenerationTarget(claim.WorkspaceID, target, request.Now)
				if planErr != nil {
					return planErr
				}
				if len(occurrences) > 0 {
					if insertErr := writer.InsertMissingOccurrences(ctx, GenerationInsert{
						WorkspaceID: claim.WorkspaceID, TaskID: target.TaskID,
						ExpectedScheduleRevision: target.ScheduleRevision, Occurrences: occurrences,
					}); insertErr != nil {
						return insertErr
					}
				}
				if completeErr := writer.CompleteGeneration(ctx, GenerationCompletion{
					WorkspaceID: claim.WorkspaceID, TaskID: target.TaskID,
					ExpectedScheduleRevision: target.ScheduleRevision,
					GenerationWatermark:      targetWatermark, Status: GenerationStatusIdle,
				}); completeErr != nil {
					return completeErr
				}
				inserted += len(occurrences)
				if targetWatermark > watermark {
					watermark = targetWatermark
				}
			}
			return nil
		})
		if writeErr != nil {
			result.Status = generationFailureStatus(writeErr)
			result.ErrorCode = GenerationClaimErrorFencedWrite
			result.RetryAt = generationClaimRetryAt(result.Status, request.Now)
			result.Error = writeErr
			results = append(results, worker.acknowledge(ctx, result))
			continue
		}
		result.Status = GenerationStatusIdle
		result.Inserted = inserted
		result.GenerationWatermark = watermark
		results = append(results, worker.acknowledge(ctx, result))
	}
	return results, nil
}

func (worker *GenerationWorker) acknowledge(ctx context.Context, result GenerationWorkspaceResult) GenerationWorkspaceResult {
	// This call is deliberately made only after BeginGenerationWrite returned,
	// so the claim store is never enlisted in the tenant transaction.
	err := worker.claims.CompleteGenerationClaim(ctx, result.GenerationClaimOutcome)
	if err == nil {
		result.Acknowledged = true
		return result
	}
	result.AckError = err
	result.Error = errors.Join(result.Error, ErrGenerationClaimAck, err)
	return result
}

func generationClaimRetryAt(status GenerationStatus, now time.Time) time.Time {
	if status != GenerationStatusRetryPending {
		return time.Time{}
	}
	return now.Add(GenerationClaimRetryDelay)
}

func planGenerationTarget(workspaceID string, target GenerationTargetState, now time.Time) ([]GenerationOccurrence, string, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(target.TaskID) == "" || target.ScheduleRevision < 1 || len(target.Versions) == 0 {
		return nil, "", ErrInvalidGenerationWorker
	}
	current, err := currentGenerationVersion(target.Versions)
	if err != nil {
		return nil, "", err
	}
	location, err := loadIANALocation(current.Schedule.Timezone)
	if err != nil {
		return nil, "", err
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.UTC)
	window := OccurrenceWindow{From: formatLocalDate(today), To: formatLocalDate(today.AddDate(0, 0, 91))}
	watermark := formatLocalDate(today.AddDate(0, 0, 90))
	if target.GenerationWatermark != "" {
		if _, parseErr := requiredGeneratorDate(target.GenerationWatermark, "generation watermark"); parseErr != nil {
			return nil, "", parseErr
		}
		if target.GenerationWatermark > watermark {
			watermark = target.GenerationWatermark
		}
	}

	byKey := make(map[string]GenerationOccurrence)
	seenRevisions := make(map[int64]struct{}, len(target.Versions))
	for _, version := range target.Versions {
		if version.Revision < 1 {
			return nil, "", ErrInvalidGenerationWorker
		}
		if _, duplicate := seenRevisions[version.Revision]; duplicate {
			return nil, "", invalidSchedule("duplicate generation schedule revision")
		}
		seenRevisions[version.Revision] = struct{}{}
		keys, calculateErr := CalculateOccurrenceKeys(version.Schedule, version.Effective, window)
		if calculateErr != nil {
			return nil, "", calculateErr
		}
		for _, key := range keys {
			localDate := key
			if key == "once" {
				localDate = version.Schedule.StartsOn
			}
			candidate := GenerationOccurrence{
				WorkspaceID: workspaceID, TaskID: target.TaskID,
				ID: DeterministicOccurrenceID(workspaceID, target.TaskID, key), OccurrenceKey: key,
				PlannedDate: localDate, GeneratedScheduleRevision: version.Revision,
			}
			if version.Schedule.TimingType == TimingTimeBlock {
				instantRange, _, resolveErr := ResolveTimeBlockUTC(
					localDate, version.Schedule.LocalStartTime, version.Schedule.Timezone,
					version.Schedule.DurationMinutes, nil,
				)
				if resolveErr != nil {
					return nil, "", resolveErr
				}
				start, end := instantRange.StartUTC, instantRange.EndUTC
				candidate.PlannedStartAt = &start
				candidate.PlannedEndAt = &end
			}
			currentCandidate, exists := byKey[key]
			if !exists || candidate.GeneratedScheduleRevision > currentCandidate.GeneratedScheduleRevision {
				byKey[key] = candidate
			}
		}
	}

	existing := make(map[string]struct{}, len(target.ExistingOccurrenceKeys))
	for _, key := range target.ExistingOccurrenceKeys {
		if strings.TrimSpace(key) == "" {
			return nil, "", ErrInvalidGenerationWorker
		}
		existing[key] = struct{}{}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		if _, exists := existing[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	occurrences := make([]GenerationOccurrence, 0, len(keys))
	for _, key := range keys {
		occurrences = append(occurrences, byKey[key])
	}
	return occurrences, watermark, nil
}

func currentGenerationVersion(versions []GenerationScheduleVersion) (GenerationScheduleVersion, error) {
	var current GenerationScheduleVersion
	for _, version := range versions {
		if version.Revision > current.Revision {
			current = version
		}
	}
	if current.Revision < 1 {
		return GenerationScheduleVersion{}, ErrInvalidGenerationWorker
	}
	return current, nil
}

func DeterministicOccurrenceID(workspaceID, taskID, occurrenceKey string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + taskID + "\x00" + occurrenceKey))
	return "occ_" + hex.EncodeToString(sum[:16])
}

func generationFailureStatus(err error) GenerationStatus {
	if errors.Is(err, ErrTaskRuntimeEpochConflict) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return GenerationStatusRetryPending
	}
	var retryable interface{ Retryable() bool }
	if errors.As(err, &retryable) && retryable.Retryable() {
		return GenerationStatusRetryPending
	}
	return GenerationStatusFailed
}
