// Package planstore persists private planning drafts in the current tenant.
// It shares the existing fenced task transaction for a confirmed plan.
package planstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

type RuntimeResolver interface {
	ResolveMobileRuntime(context.Context, string) (taskruntime.MobileRuntimeSnapshot, error)
}

type Repository struct {
	runtime RuntimeResolver
	now     func() time.Time
}

func New(runtime RuntimeResolver) (*Repository, error) {
	if runtime == nil {
		return nil, webplanning.ErrUnavailable
	}
	return &Repository{runtime: runtime, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *Repository) resolve(ctx context.Context, scope webplanning.Scope) (taskruntime.MobileRuntimeSnapshot, error) {
	if r == nil || r.runtime == nil || strings.TrimSpace(scope.WorkspaceID) == "" {
		return taskruntime.MobileRuntimeSnapshot{}, webplanning.ErrInvalid
	}
	runtime, err := r.runtime.ResolveMobileRuntime(ctx, scope.WorkspaceID)
	if err != nil {
		return runtime, err
	}
	if runtime.WorkspaceID != scope.WorkspaceID || runtime.Epoch < 1 || runtime.DB == nil || runtime.Writer == nil || runtime.Application.WorkspaceID != scope.WorkspaceID || runtime.Application.Epoch != runtime.Epoch {
		return runtime, webplanning.ErrUnavailable
	}
	if runtime.Driver != storage.DriverSQLite && runtime.Driver != storage.DriverPostgres {
		return runtime, webplanning.ErrUnavailable
	}
	return runtime, nil
}

func (r *Repository) Read(ctx context.Context, scope webplanning.Scope, fn func(webplanning.Reader) error) error {
	if fn == nil {
		return webplanning.ErrInvalid
	}
	runtime, err := r.resolve(ctx, scope)
	if err != nil {
		return err
	}
	return fn(&reader{scope: scope, runner: runtime.DB, runtime: runtime, now: r.now})
}

func (r *Repository) Write(ctx context.Context, scope webplanning.Scope, fn func(webplanning.Tx) error) error {
	if fn == nil || scope.ActorID == "" {
		return webplanning.ErrInvalid
	}
	runtime, err := r.resolve(ctx, scope)
	if err != nil {
		return err
	}
	writer, ok := runtime.Writer.(storage.MobileV2TenantFencedWriter)
	if !ok {
		return webplanning.ErrUnavailable
	}
	return writer.BeginFencedMobileV2Write(ctx, scope.WorkspaceID, runtime.Epoch, func(tx storage.MobileV2TenantWriteTx) error {
		application, err := taskruntime.NewTransactionApplication(tx, scope.WorkspaceID, runtime.Epoch)
		if err != nil {
			return err
		}
		runtime.Application = application
		return fn(&transaction{reader: reader{scope: scope, runner: tx.MobileV2SQLRunner(), runtime: runtime, now: r.now}, tx: tx})
	})
}

func (r *Repository) Commit(ctx context.Context, scope webplanning.Scope, request webplanning.CommitRequest) (webplanning.Receipt, error) {
	var receipt webplanning.Receipt
	if scope.ActorID == "" {
		return receipt, webplanning.ErrInvalid
	}
	runtime, err := r.resolve(ctx, scope)
	if err != nil {
		return receipt, err
	}
	err = runtime.Writer.BeginFencedWrite(ctx, scope.WorkspaceID, runtime.Epoch, func(tx storage.TenantWriteTx) error {
		mobileTx, ok := tx.(storage.MobileV2TenantWriteTx)
		if !ok {
			return webplanning.ErrUnavailable
		}
		application, err := taskruntime.NewTransactionApplication(mobileTx, scope.WorkspaceID, runtime.Epoch)
		if err != nil {
			return err
		}
		runtime.Application = application
		store := &transaction{reader: reader{scope: scope, runner: mobileTx.MobileV2SQLRunner(), runtime: runtime, now: r.now}, tx: mobileTx, taskWrite: true}
		receipt, err = store.CommitPlan(ctx, request)
		return err
	})
	return receipt, err
}

type reader struct {
	scope   webplanning.Scope
	runner  storage.TenantSQLRunner
	runtime taskruntime.MobileRuntimeSnapshot
	now     func() time.Time
}

func (r *reader) bind(query string) string {
	if r.runtime.Driver != storage.DriverPostgres {
		return query
	}
	var out strings.Builder
	n := 1
	for _, c := range query {
		if c == '?' {
			fmt.Fprintf(&out, "$%d", n)
			n++
		} else {
			out.WriteRune(c)
		}
	}
	return out.String()
}

func (r *reader) owned() error {
	if r.scope.ActorID == "" {
		return webplanning.ErrInvalid
	}
	return nil
}

func (r *reader) draft(ctx context.Context, field, value string) (webplanning.Draft, error) {
	var draft webplanning.Draft
	if err := r.owned(); err != nil {
		return draft, err
	}
	var record string
	err := r.runner.QueryRowContext(ctx, r.bind(`SELECT record_json FROM plan_drafts WHERE workspace_id=? AND owner_id=? AND `+field+`=?`), r.scope.WorkspaceID, r.scope.ActorID, value).Scan(&record)
	if err != nil {
		return draft, missing(err)
	}
	return decodeDraft(record)
}

func (r *reader) GetDraft(ctx context.Context, id string) (webplanning.Draft, error) {
	return r.draft(ctx, "id", id)
}
func (r *reader) FindDraftByRequestID(ctx context.Context, id string) (webplanning.Draft, error) {
	return r.draft(ctx, "request_id", id)
}

func (r *reader) ListDrafts(ctx context.Context) ([]webplanning.Draft, error) {
	query := `SELECT record_json FROM plan_drafts WHERE workspace_id=?`
	args := []any{r.scope.WorkspaceID}
	if r.scope.ActorID != "" {
		query += ` AND owner_id=? ORDER BY updated_at DESC LIMIT 100`
		args = append(args, r.scope.ActorID)
	} else {
		query += ` AND (cleanup_needed=1 OR (audio_expires_at<>'' AND audio_expires_at<=?)) ORDER BY updated_at`
		args = append(args, stamp(r.now()))
	}
	rows, err := r.runner.QueryContext(ctx, r.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := []webplanning.Draft{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		draft, err := decodeDraft(raw)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

func (r *reader) job(ctx context.Context, field, value string) (webplanning.Job, error) {
	var job webplanning.Job
	if err := r.owned(); err != nil {
		return job, err
	}
	var record string
	err := r.runner.QueryRowContext(ctx, r.bind(`SELECT record_json FROM plan_jobs WHERE workspace_id=? AND owner_id=? AND `+field+`=?`), r.scope.WorkspaceID, r.scope.ActorID, value).Scan(&record)
	if err != nil {
		return job, missing(err)
	}
	return decodeJob(record)
}
func (r *reader) GetJob(ctx context.Context, id string) (webplanning.Job, error) {
	return r.job(ctx, "id", id)
}
func (r *reader) FindJobByRequestID(ctx context.Context, id string) (webplanning.Job, error) {
	return r.job(ctx, "request_id", id)
}

func (r *reader) ListEligibleJobs(ctx context.Context, now time.Time, limit int) ([]webplanning.Job, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	query := `SELECT record_json FROM plan_jobs WHERE workspace_id=? AND (state='queued' OR (state='processing' AND lease_expires_at<=?))`
	args := []any{r.scope.WorkspaceID, stamp(now)}
	if r.scope.ActorID != "" {
		query += ` AND owner_id=?`
		args = append(args, r.scope.ActorID)
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, limit)
	rows, err := r.runner.QueryContext(ctx, r.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []webplanning.Job{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		job, err := decodeJob(raw)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *reader) receipt(ctx context.Context, field, id string) (webplanning.Receipt, error) {
	var receipt webplanning.Receipt
	if err := r.owned(); err != nil {
		return receipt, err
	}
	var raw string
	var created string
	err := r.runner.QueryRowContext(ctx, r.bind(`SELECT receipt_json,request_id,request_hash,created_at FROM plan_commits WHERE workspace_id=? AND owner_id=? AND `+field+`=?`), r.scope.WorkspaceID, r.scope.ActorID, id).Scan(&raw, &receipt.RequestID, &receipt.RequestHash, &created)
	if err != nil {
		return receipt, missing(err)
	}
	metaID, metaHash := receipt.RequestID, receipt.RequestHash
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return receipt, err
	}
	receipt.RequestID, receipt.RequestHash = metaID, metaHash
	receipt.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return receipt, nil
}
func (r *reader) GetCommitByDraft(ctx context.Context, id string) (webplanning.Receipt, error) {
	return r.receipt(ctx, "draft_id", id)
}
func (r *reader) GetCommitByKey(ctx context.Context, id string) (webplanning.Receipt, error) {
	return r.receipt(ctx, "request_id", id)
}

type transaction struct {
	reader
	tx        storage.MobileV2TenantWriteTx
	taskWrite bool
}

func (t *transaction) PutDraft(ctx context.Context, draft webplanning.Draft, expected int64) error {
	if draft.WorkspaceID != t.scope.WorkspaceID || draft.OwnerID != t.scope.ActorID || draft.ID == "" || draft.RequestID == "" || draft.Revision != expected+1 || expected < 0 {
		return webplanning.ErrInvalid
	}
	raw, err := encodeDraft(draft)
	if err != nil {
		return err
	}
	if expected == 0 {
		result, err := t.runner.ExecContext(ctx, t.bind(`INSERT INTO plan_drafts(workspace_id,owner_id,id,request_id,request_hash,revision,status,record_json,created_at,updated_at,cleanup_needed,audio_expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`), draft.WorkspaceID, draft.OwnerID, draft.ID, draft.RequestID, draft.RequestHash, draft.Revision, draft.Status, raw, stamp(draft.CreatedAt), stamp(draft.UpdatedAt), cleanupNeeded(draft), audioExpiry(draft))
		return changed(result, err)
	}
	current, err := t.GetDraft(ctx, draft.ID)
	if err != nil {
		return err
	}
	if current.Status == "committed" || current.Status == "discarded" {
		if !reflect.DeepEqual(immutableDraft(current), immutableDraft(draft)) {
			return webplanning.ErrConflict
		}
	}
	result, err := t.runner.ExecContext(ctx, t.bind(`UPDATE plan_drafts SET revision=?,status=?,record_json=?,updated_at=?,cleanup_needed=?,audio_expires_at=? WHERE workspace_id=? AND owner_id=? AND id=? AND revision=? AND request_id=? AND request_hash=?`), draft.Revision, draft.Status, raw, stamp(draft.UpdatedAt), cleanupNeeded(draft), audioExpiry(draft), draft.WorkspaceID, draft.OwnerID, draft.ID, expected, draft.RequestID, draft.RequestHash)
	return changed(result, err)
}

func (t *transaction) PutJob(ctx context.Context, job webplanning.Job, expected int64) error {
	if job.WorkspaceID != t.scope.WorkspaceID || job.OwnerID != t.scope.ActorID || job.ID == "" || job.RequestID == "" || job.Revision != expected+1 || expected < 0 {
		return webplanning.ErrInvalid
	}
	raw, err := encodeJob(job)
	if err != nil {
		return err
	}
	if expected == 0 {
		result, err := t.runner.ExecContext(ctx, t.bind(`INSERT INTO plan_jobs(workspace_id,owner_id,id,draft_id,request_id,request_hash,revision,state,lease_expires_at,record_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`), job.WorkspaceID, job.OwnerID, job.ID, job.DraftID, job.RequestID, job.RequestHash, job.Revision, job.State, stamp(job.LeaseUntil), raw, stamp(job.CreatedAt), stamp(job.UpdatedAt))
		return changed(result, err)
	}
	result, err := t.runner.ExecContext(ctx, t.bind(`UPDATE plan_jobs SET revision=?,state=?,lease_expires_at=?,record_json=?,updated_at=? WHERE workspace_id=? AND owner_id=? AND id=? AND revision=? AND request_id=? AND request_hash=?`), job.Revision, job.State, stamp(job.LeaseUntil), raw, stamp(job.UpdatedAt), job.WorkspaceID, job.OwnerID, job.ID, expected, job.RequestID, job.RequestHash)
	return changed(result, err)
}

func missing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return webplanning.ErrNotFound
	}
	return err
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return webplanning.ErrConflict
	}
	return nil
}
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

var _ webplanning.Repository = (*Repository)(nil)

func cleanupNeeded(d webplanning.Draft) int {
	if len(d.CleanupObjectKeys) > 0 || (d.AudioObjectKey != "" && (d.Status == "committed" || d.Status == "discarded")) {
		return 1
	}
	return 0
}
func audioExpiry(d webplanning.Draft) string {
	if d.AudioExpiresAt == nil || d.AudioObjectKey == "" {
		return ""
	}
	return stamp(*d.AudioExpiresAt)
}
func immutableDraft(d webplanning.Draft) webplanning.Draft {
	d.Revision = 0
	d.UpdatedAt = time.Time{}
	d.AudioObjectKey = ""
	d.AudioMIME = ""
	d.AudioFilename = ""
	d.AudioDigest = ""
	d.AudioSize = 0
	d.AudioExpiresAt = nil
	d.AudioAvailable = false
	d.AudioPending = false
	d.AudioCleanupAfter = time.Time{}
	d.CleanupObjectKeys = nil
	return d
}
