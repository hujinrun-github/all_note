package generationclaims

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var (
	ErrInvalidRepository = errors.New("invalid generation claim repository")
	ErrInvalidJob        = errors.New("invalid generation job")
	ErrInvalidClaim      = errors.New("invalid generation claim outcome")
	ErrJobNotFound       = errors.New("generation job not found")
	ErrJobConflict       = errors.New("generation job conflict")
	ErrClaimConflict     = errors.New("generation claim conflict")
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type Status string

const (
	StatusQueued       Status = "queued"
	StatusClaimed      Status = "claimed"
	StatusRetryPending Status = "retry_pending"
	StatusCompleted    Status = "completed"
	StatusFailed       Status = "failed"
)

const defaultLeaseDuration = time.Minute

type Option func(*Repository) error

func WithLeaseDuration(duration time.Duration) Option {
	return func(repository *Repository) error {
		if duration <= 0 {
			return ErrInvalidRepository
		}
		repository.leaseDuration = duration
		return nil
	}
}

type EnqueueRequest struct {
	JobID        string
	WorkspaceID  string
	CreatedEpoch int64
	AvailableAt  time.Time
}

type RescheduleRequest struct {
	WorkspaceID  string
	CreatedEpoch int64
	AvailableAt  time.Time
}

type Job struct {
	JobID               string
	WorkspaceID         string
	ClaimID             string
	CreatedEpoch        int64
	Status              Status
	Attempt             int
	AvailableAt         time.Time
	LeaseUntil          time.Time
	RuntimeEpoch        int64
	Inserted            int
	GenerationWatermark string
	ErrorCode           taskdomain.GenerationClaimErrorCode
	Revision            int64
}

type Repository struct {
	db            *sql.DB
	dialect       Dialect
	leaseDuration time.Duration
}

func New(db *sql.DB, dialect Dialect, options ...Option) (*Repository, error) {
	if db == nil || (dialect != DialectSQLite && dialect != DialectPostgres) {
		return nil, ErrInvalidRepository
	}
	repository := &Repository{db: db, dialect: dialect, leaseDuration: defaultLeaseDuration}
	for _, option := range options {
		if option == nil {
			return nil, ErrInvalidRepository
		}
		if err := option(repository); err != nil {
			return nil, err
		}
	}
	return repository, nil
}

func (r *Repository) Enqueue(ctx context.Context, request EnqueueRequest) error {
	request.JobID = strings.TrimSpace(request.JobID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.AvailableAt = normalizeTime(request.AvailableAt)
	if request.JobID == "" || request.WorkspaceID == "" || request.CreatedEpoch < 1 || request.AvailableAt.IsZero() {
		return ErrInvalidJob
	}
	result, err := r.db.ExecContext(ctx, r.bind(`INSERT INTO task_domain_generation_jobs(
		job_id,workspace_id,created_epoch,status,attempt,available_at,revision
	) VALUES(?,?,?,'queued',0,?,1) ON CONFLICT DO NOTHING`), request.JobID, request.WorkspaceID, request.CreatedEpoch, r.timeArg(request.AvailableAt))
	if err != nil {
		return fmt.Errorf("enqueue generation job: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect generation enqueue: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	existing, err := r.Get(ctx, request.WorkspaceID)
	if err != nil {
		if errors.Is(err, ErrJobNotFound) {
			return ErrJobConflict
		}
		return err
	}
	if existing.JobID == request.JobID && existing.CreatedEpoch == request.CreatedEpoch &&
		existing.Status == StatusQueued && existing.AvailableAt.Equal(request.AvailableAt) {
		return nil
	}
	return ErrJobConflict
}

// EnsureScheduled creates the single durable generation cycle for a workspace,
// or starts a new cycle after a terminal one. Pending retries and live leases
// are deliberately left untouched so a periodic scan cannot erase backoff or
// steal work from an active worker. The return value reports whether durable
// state was inserted or rescheduled.
func (r *Repository) EnsureScheduled(ctx context.Context, request EnqueueRequest) (bool, error) {
	request.JobID = strings.TrimSpace(request.JobID)
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.AvailableAt = normalizeTime(request.AvailableAt)
	if request.JobID == "" || request.WorkspaceID == "" || request.CreatedEpoch < 1 || request.AvailableAt.IsZero() {
		return false, ErrInvalidJob
	}
	result, err := r.db.ExecContext(ctx, r.bind(`INSERT INTO task_domain_generation_jobs(
		job_id,workspace_id,created_epoch,status,attempt,available_at,revision
	) VALUES(?,?,?,'queued',0,?,1)
	ON CONFLICT(workspace_id) DO UPDATE SET
		job_id=excluded.job_id,created_epoch=excluded.created_epoch,status='queued',attempt=0,
		available_at=excluded.available_at,claim_id=NULL,lease_until=NULL,runtime_epoch=NULL,
		inserted=0,generation_watermark=NULL,error_code=NULL,
		revision=task_domain_generation_jobs.revision+1,updated_at=CURRENT_TIMESTAMP
	WHERE task_domain_generation_jobs.status IN ('completed','failed')`),
		request.JobID, request.WorkspaceID, request.CreatedEpoch, r.timeArg(request.AvailableAt))
	if err != nil {
		return false, fmt.Errorf("ensure generation job scheduled: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect ensured generation job: %w", err)
	}
	return count == 1, nil
}

// Reschedule begins a new periodic cycle. It deliberately refuses to steal a
// live lease; a scheduler must wait for its acknowledgement or expiry first.
func (r *Repository) Reschedule(ctx context.Context, request RescheduleRequest) error {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.AvailableAt = normalizeTime(request.AvailableAt)
	if request.WorkspaceID == "" || request.CreatedEpoch < 1 || request.AvailableAt.IsZero() {
		return ErrInvalidJob
	}
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE task_domain_generation_jobs SET
		created_epoch=?,status='queued',attempt=0,available_at=?,claim_id=NULL,lease_until=NULL,
		runtime_epoch=NULL,inserted=0,generation_watermark=NULL,error_code=NULL,
		revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND status<>'claimed'`), request.CreatedEpoch, r.timeArg(request.AvailableAt), request.WorkspaceID)
	if err != nil {
		return fmt.Errorf("reschedule generation job: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect generation reschedule: %w", err)
	}
	if count == 1 {
		return nil
	}
	job, getErr := r.Get(ctx, request.WorkspaceID)
	if errors.Is(getErr, ErrJobNotFound) {
		return ErrJobNotFound
	}
	if getErr != nil {
		return getErr
	}
	if job.Status == StatusClaimed {
		return ErrClaimConflict
	}
	return ErrJobConflict
}

func (r *Repository) Get(ctx context.Context, workspaceID string) (Job, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return Job{}, ErrInvalidJob
	}
	row := r.db.QueryRowContext(ctx, r.bind(`SELECT
		job_id,workspace_id,claim_id,created_epoch,status,attempt,available_at,lease_until,
		runtime_epoch,inserted,generation_watermark,error_code,revision
		FROM task_domain_generation_jobs WHERE workspace_id=?`), workspaceID)
	job, err := r.scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("read generation job: %w", err)
	}
	return job, nil
}

func (r *Repository) ClaimGenerationWorkspaces(ctx context.Context, limit int, claimedAt time.Time) ([]taskdomain.GenerationWorkspaceClaim, error) {
	claimedAt = normalizeTime(claimedAt)
	if limit < 1 || claimedAt.IsZero() {
		return nil, ErrInvalidClaim
	}
	claims := make([]taskdomain.GenerationWorkspaceClaim, 0, limit)
	for len(claims) < limit {
		claimID := uuid.NewString()
		leaseUntil := normalizeTime(claimedAt.Add(r.leaseDuration))
		claim, found, err := r.claimOne(ctx, claimID, claimedAt, leaseUntil)
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func (r *Repository) claimOne(ctx context.Context, claimID string, claimedAt, leaseUntil time.Time) (taskdomain.GenerationWorkspaceClaim, bool, error) {
	var query string
	if r.dialect == DialectPostgres {
		query = postgresClaimOneSQL
	} else {
		query = `UPDATE task_domain_generation_jobs SET
			status='claimed',claim_id=?,attempt=attempt+1,lease_until=?,
			runtime_epoch=NULL,inserted=0,generation_watermark=NULL,error_code=NULL,
			revision=revision+1,updated_at=?
			WHERE job_id=(SELECT job_id FROM task_domain_generation_jobs
				WHERE ((status IN ('queued','retry_pending') AND available_at<=?)
					OR (status='claimed' AND lease_until<=?))
				ORDER BY available_at,workspace_id LIMIT 1)
			RETURNING workspace_id,created_epoch`
	}
	var row *sql.Row
	if r.dialect == DialectPostgres {
		row = r.db.QueryRowContext(ctx, query, r.timeArg(claimedAt), claimID, r.timeArg(leaseUntil))
	} else {
		row = r.db.QueryRowContext(ctx, query, claimID, r.timeArg(leaseUntil), r.timeArg(claimedAt), r.timeArg(claimedAt), r.timeArg(claimedAt))
	}
	claim := taskdomain.GenerationWorkspaceClaim{ClaimID: claimID}
	if err := row.Scan(&claim.WorkspaceID, &claim.CreatedEpoch); errors.Is(err, sql.ErrNoRows) {
		return taskdomain.GenerationWorkspaceClaim{}, false, nil
	} else if err != nil {
		return taskdomain.GenerationWorkspaceClaim{}, false, fmt.Errorf("claim generation job: %w", err)
	}
	return claim, true, nil
}

const postgresClaimOneSQL = `WITH candidate AS (
	SELECT job_id FROM task_domain_generation_jobs
	WHERE ((status IN ('queued','retry_pending') AND available_at<=$1)
		OR (status='claimed' AND lease_until<=$1))
	ORDER BY available_at,workspace_id
	FOR UPDATE SKIP LOCKED LIMIT 1
) UPDATE task_domain_generation_jobs AS jobs SET
	status='claimed',claim_id=$2,attempt=attempt+1,lease_until=$3,
	runtime_epoch=NULL,inserted=0,generation_watermark=NULL,error_code=NULL,
	revision=revision+1,updated_at=$1
FROM candidate WHERE jobs.job_id=candidate.job_id
RETURNING jobs.workspace_id,jobs.created_epoch`

func (r *Repository) CompleteGenerationClaim(ctx context.Context, outcome taskdomain.GenerationClaimOutcome) error {
	outcome.ClaimID = strings.TrimSpace(outcome.ClaimID)
	outcome.WorkspaceID = strings.TrimSpace(outcome.WorkspaceID)
	outcome.GenerationWatermark = strings.TrimSpace(outcome.GenerationWatermark)
	outcome.RetryAt = normalizeTime(outcome.RetryAt)
	if err := validateOutcome(outcome); err != nil {
		return err
	}
	current, err := r.Get(ctx, outcome.WorkspaceID)
	if err != nil {
		return err
	}
	if current.ClaimID != outcome.ClaimID || current.CreatedEpoch != outcome.CreatedEpoch {
		return ErrClaimConflict
	}
	target := outcomeStatus(outcome.Status)
	if current.Status == target {
		if outcomeMatches(current, outcome) {
			return nil
		}
		return ErrClaimConflict
	}
	if current.Status != StatusClaimed {
		return ErrClaimConflict
	}

	availableAt := current.AvailableAt
	if target == StatusRetryPending {
		availableAt = outcome.RetryAt
	}
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE task_domain_generation_jobs SET
		status=?,available_at=?,lease_until=NULL,runtime_epoch=?,inserted=?,generation_watermark=?,error_code=?,
		revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND claim_id=? AND created_epoch=? AND status='claimed' AND revision=?`),
		target, r.timeArg(availableAt), nullablePositive(outcome.RuntimeEpoch), outcome.Inserted,
		nullableString(outcome.GenerationWatermark), nullableString(string(outcome.ErrorCode)),
		outcome.WorkspaceID, outcome.ClaimID, outcome.CreatedEpoch, current.Revision)
	if err != nil {
		return fmt.Errorf("complete generation claim: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect generation claim completion: %w", err)
	}
	if count == 1 {
		return nil
	}
	latest, getErr := r.Get(ctx, outcome.WorkspaceID)
	if getErr == nil && latest.ClaimID == outcome.ClaimID && latest.CreatedEpoch == outcome.CreatedEpoch &&
		latest.Status == target && outcomeMatches(latest, outcome) {
		return nil
	}
	return ErrClaimConflict
}

func validateOutcome(outcome taskdomain.GenerationClaimOutcome) error {
	if outcome.ClaimID == "" || outcome.WorkspaceID == "" || outcome.CreatedEpoch < 1 || outcome.RuntimeEpoch < 0 || outcome.Inserted < 0 {
		return ErrInvalidClaim
	}
	switch outcome.Status {
	case taskdomain.GenerationStatusIdle:
		if outcome.RuntimeEpoch < 1 || !outcome.RetryAt.IsZero() || outcome.ErrorCode != taskdomain.GenerationClaimErrorNone {
			return ErrInvalidClaim
		}
	case taskdomain.GenerationStatusRetryPending:
		if outcome.RetryAt.IsZero() || !canonicalErrorCode(outcome.ErrorCode) {
			return ErrInvalidClaim
		}
	case taskdomain.GenerationStatusFailed:
		if !outcome.RetryAt.IsZero() || !canonicalErrorCode(outcome.ErrorCode) {
			return ErrInvalidClaim
		}
	default:
		return ErrInvalidClaim
	}
	return nil
}

func canonicalErrorCode(code taskdomain.GenerationClaimErrorCode) bool {
	switch code {
	case taskdomain.GenerationClaimErrorInvalidRuntime,
		taskdomain.GenerationClaimErrorRuntimeResolve,
		taskdomain.GenerationClaimErrorFencedWrite:
		return true
	default:
		return false
	}
}

func outcomeStatus(status taskdomain.GenerationStatus) Status {
	if status == taskdomain.GenerationStatusIdle {
		return StatusCompleted
	}
	return Status(status)
}

func outcomeMatches(job Job, outcome taskdomain.GenerationClaimOutcome) bool {
	if job.RuntimeEpoch != outcome.RuntimeEpoch || job.Inserted != outcome.Inserted ||
		job.GenerationWatermark != outcome.GenerationWatermark || job.ErrorCode != outcome.ErrorCode {
		return false
	}
	if outcome.Status == taskdomain.GenerationStatusRetryPending {
		return job.AvailableAt.Equal(outcome.RetryAt)
	}
	return true
}

type rowScanner interface {
	Scan(...any) error
}

func (r *Repository) scanJob(row rowScanner) (Job, error) {
	var job Job
	var claimID, watermark, errorCode sql.NullString
	var runtimeEpoch sql.NullInt64
	if r.dialect == DialectPostgres {
		var availableAt time.Time
		var leaseUntil sql.NullTime
		err := row.Scan(&job.JobID, &job.WorkspaceID, &claimID, &job.CreatedEpoch, &job.Status, &job.Attempt,
			&availableAt, &leaseUntil, &runtimeEpoch, &job.Inserted, &watermark, &errorCode, &job.Revision)
		if err != nil {
			return Job{}, err
		}
		job.AvailableAt = normalizeTime(availableAt)
		if leaseUntil.Valid {
			job.LeaseUntil = normalizeTime(leaseUntil.Time)
		}
	} else {
		var availableAt string
		var leaseUntil sql.NullString
		err := row.Scan(&job.JobID, &job.WorkspaceID, &claimID, &job.CreatedEpoch, &job.Status, &job.Attempt,
			&availableAt, &leaseUntil, &runtimeEpoch, &job.Inserted, &watermark, &errorCode, &job.Revision)
		if err != nil {
			return Job{}, err
		}
		parsed, err := parseSQLiteTime(availableAt)
		if err != nil {
			return Job{}, err
		}
		job.AvailableAt = parsed
		if leaseUntil.Valid {
			parsed, err = parseSQLiteTime(leaseUntil.String)
			if err != nil {
				return Job{}, err
			}
			job.LeaseUntil = parsed
		}
	}
	job.ClaimID = claimID.String
	job.RuntimeEpoch = runtimeEpoch.Int64
	job.GenerationWatermark = watermark.String
	job.ErrorCode = taskdomain.GenerationClaimErrorCode(errorCode.String)
	return job, nil
}

const sqliteTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func normalizeTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC().Truncate(time.Microsecond)
}

func (r *Repository) timeArg(value time.Time) any {
	value = normalizeTime(value)
	if r.dialect == DialectSQLite {
		return value.Format(sqliteTimeLayout)
	}
	return value
}

func parseSQLiteTime(value string) (time.Time, error) {
	parsed, err := time.Parse(sqliteTimeLayout, value)
	if err == nil {
		return normalizeTime(parsed), nil
	}
	parsed, fallbackErr := time.Parse(time.RFC3339Nano, value)
	if fallbackErr != nil {
		return time.Time{}, fmt.Errorf("parse generation job time %q: %w", value, err)
	}
	return normalizeTime(parsed), nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullablePositive(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

func (r *Repository) bind(query string) string {
	if r.dialect == DialectSQLite {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}
