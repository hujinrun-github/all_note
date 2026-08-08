package contentimportdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

type Runner interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

type Repository struct {
	db      Runner
	dialect Dialect
}

func New(db Runner, dialect Dialect) *Repository {
	return &Repository{db: db, dialect: dialect}
}

func (r *Repository) bind(query string) string {
	if r.dialect != Postgres {
		return query
	}
	var result strings.Builder
	parameter := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&result, "$%d", parameter)
			parameter++
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}

func (r *Repository) CreateOrGet(ctx context.Context, request model.CreateContentImport) (*model.ContentImport, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectIDs, _ := json.Marshal(request.ProjectIDs)
	tags, _ := json.Marshal(request.Tags)
	query := r.bind(`
		INSERT INTO content_imports (
			id,workspace_id,idempotency_key,request_sha256,source_url,status,stage,progress,
			summarize_with_ai,summary_prompt,include_transcript,language,folder_id,project_ids,tags,
			revision,attempt,max_attempts,created_at,updated_at
		) VALUES (?,?,?,?,?,'active','queued',0,?,?,?,?,?,?,?,1,0,4,?,?)
		ON CONFLICT (workspace_id,idempotency_key) DO NOTHING
	`)
	if _, err := r.db.ExecContext(ctx, query,
		request.ID, workspaceID, request.IdempotencyKey, request.RequestSHA256, request.SourceURL,
		request.SummarizeWithAI, request.SummaryPrompt, request.IncludeTranscript, request.Language, request.FolderID,
		string(projectIDs), string(tags), request.Now, request.Now,
	); err != nil {
		return nil, err
	}
	var storedHash, id string
	if err := r.db.QueryRowContext(ctx, r.bind(`
		SELECT id,request_sha256 FROM content_imports WHERE workspace_id=? AND idempotency_key=?
	`), workspaceID, request.IdempotencyKey).Scan(&id, &storedHash); err != nil {
		return nil, err
	}
	if storedHash != request.RequestSHA256 {
		return nil, storage.ErrMutationIDReused
	}
	return r.Get(ctx, id)
}

func (r *Repository) List(ctx context.Context, filter model.ContentImportFilter) ([]model.ContentImport, int, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	where := "workspace_id=?"
	args := []interface{}{workspaceID}
	if strings.TrimSpace(filter.Status) != "" && filter.Status != "all" {
		where += " AND status=?"
		args = append(args, filter.Status)
	}
	var total int
	if err := r.db.QueryRowContext(ctx, r.bind("SELECT COUNT(*) FROM content_imports WHERE "+where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := r.db.QueryContext(ctx, r.bind(contentImportSelect+" WHERE "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	imports := make([]model.ContentImport, 0)
	for rows.Next() {
		item, err := scanContentImport(rows)
		if err != nil {
			return nil, 0, err
		}
		imports = append(imports, *item)
	}
	return imports, total, rows.Err()
}

func (r *Repository) Get(ctx context.Context, id string) (*model.ContentImport, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanContentImport(r.db.QueryRowContext(ctx, r.bind(contentImportSelect+" WHERE workspace_id=? AND id=?"), workspaceID, id))
}

func (r *Repository) GetByResultNoteID(ctx context.Context, noteID string) (*model.ContentImport, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return scanContentImport(r.db.QueryRowContext(ctx, r.bind(
		contentImportSelect+" WHERE workspace_id=? AND result_note_id=? AND status='completed' ORDER BY updated_at DESC LIMIT 1",
	), workspaceID, noteID))
}

func (r *Repository) Cancel(ctx context.Context, id string, now int64) (*model.ContentImport, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET status='canceled',stage='terminal',error_code='canceled_by_user',
			error_message='',lease_owner='',lease_token='',lease_expires_at=NULL,revision=revision+1,updated_at=?
		WHERE workspace_id=? AND id=? AND status='active'
	`), now, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportNotRetryable
	}
	return r.Get(ctx, id)
}

func (r *Repository) Retry(ctx context.Context, id string, now int64) (*model.ContentImport, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET status='active',stage='queued',progress=0,error_code='',error_message='',
			lease_owner='',lease_token='',lease_expires_at=NULL,attempt=attempt+1,revision=revision+1,updated_at=?
		WHERE workspace_id=? AND id=? AND status IN ('failed','needs_review') AND attempt<max_attempts
	`), now, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportNotRetryable
	}
	return r.Get(ctx, id)
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	item, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	if item.Status == model.ContentImportStatusActive {
		return storage.ErrContentImportNotDeletable
	}
	result, err := r.db.ExecContext(ctx, r.bind(`
		DELETE FROM content_imports
		WHERE workspace_id=? AND id=? AND status IN ('completed','failed','needs_review','canceled')
	`), workspaceID, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return storage.ErrContentImportNotDeletable
	}
	return nil
}

func (r *Repository) ClaimNext(ctx context.Context, claim model.ClaimContentImport) (*model.ContentImportLease, error) {
	query := r.bind(`
		UPDATE content_imports SET stage=CASE WHEN stage='queued' THEN 'resolving' ELSE stage END,
			progress=CASE WHEN stage='queued' THEN 8 ELSE progress END,
			lease_owner=?,lease_token=?,lease_expires_at=?,revision=revision+1,updated_at=?
		WHERE id=(
			SELECT id FROM content_imports
			WHERE status='active' AND (stage='queued' OR lease_expires_at IS NULL OR lease_expires_at<=?)
			ORDER BY created_at ASC LIMIT 1
		)
		RETURNING workspace_id,lease_expires_at,` + contentImportColumns + `
	`)
	row := r.db.QueryRowContext(ctx, query, claim.WorkerID, claim.LeaseToken, claim.LeaseExpiresAt, claim.Now, claim.Now)
	var workspaceID string
	var leaseExpiresAt int64
	item, err := scanContentImportWithPrefix(row, &workspaceID, &leaseExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNoContentImport
	}
	if err != nil {
		return nil, err
	}
	return &model.ContentImportLease{Import: *item, WorkspaceID: workspaceID, LeaseToken: claim.LeaseToken, LeaseExpiresAt: leaseExpiresAt}, nil
}

func (r *Repository) UpdateResolved(ctx context.Context, update model.UpdateContentImportResolved) (*model.ContentImport, error) {
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET source_type=?,canonical_url=?,external_id=?,feed_url=?,title=?,podcast_title=?,
			cover_url=?,description=?,duration_seconds=?,transcript_url=?,audio_url=?,stage='acquiring',progress=35,
			revision=revision+1,updated_at=? WHERE id=? AND lease_token=? AND status='active'
	`), update.SourceType, update.CanonicalURL, update.ExternalID, update.FeedURL, update.Title, update.PodcastTitle,
		update.CoverURL, update.Description, update.DurationSeconds, update.TranscriptURL, update.AudioURL, update.Now, update.ID, update.LeaseToken)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportLeaseLost
	}
	return r.getByLease(ctx, update.ID, update.LeaseToken)
}

func (r *Repository) UpdateStage(ctx context.Context, update model.UpdateContentImportStage) (*model.ContentImport, error) {
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET stage=?,progress=?,revision=revision+1,updated_at=?
		WHERE id=? AND lease_token=? AND status='active'
	`), update.Stage, update.Progress, update.Now, update.ID, update.LeaseToken)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportLeaseLost
	}
	return r.getByLease(ctx, update.ID, update.LeaseToken)
}

func (r *Repository) Heartbeat(ctx context.Context, heartbeat model.HeartbeatContentImport) error {
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET lease_expires_at=?,updated_at=?
		WHERE id=? AND lease_token=? AND status='active'
	`), heartbeat.LeaseExpiresAt, heartbeat.Now, heartbeat.ID, heartbeat.LeaseToken)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return storage.ErrContentImportLeaseLost
	}
	return nil
}

func (r *Repository) PutArtifact(ctx context.Context, artifact model.ContentImportArtifact) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, r.bind(`
		INSERT INTO content_import_artifacts (workspace_id,import_id,kind,inline_text,sha256,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (workspace_id,import_id,kind) DO UPDATE SET inline_text=excluded.inline_text,sha256=excluded.sha256,updated_at=excluded.updated_at
	`), workspaceID, artifact.ImportID, artifact.Kind, artifact.Text, artifact.SHA256, artifact.CreatedAt, artifact.UpdatedAt)
	return err
}

func (r *Repository) GetArtifact(ctx context.Context, importID, kind string) (*model.ContentImportArtifact, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	var artifact model.ContentImportArtifact
	err = r.db.QueryRowContext(ctx, r.bind(`
		SELECT import_id,kind,inline_text,sha256,created_at,updated_at FROM content_import_artifacts
		WHERE workspace_id=? AND import_id=? AND kind=?
	`), workspaceID, importID, kind).Scan(&artifact.ImportID, &artifact.Kind, &artifact.Text, &artifact.SHA256, &artifact.CreatedAt, &artifact.UpdatedAt)
	return &artifact, err
}

func (r *Repository) Fail(ctx context.Context, failure model.FailContentImport) (*model.ContentImport, error) {
	status := failure.Status
	if status != model.ContentImportStatusNeedsReview {
		status = model.ContentImportStatusFailed
	}
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET status=?,stage='terminal',progress=100,error_code=?,error_message=?,
			lease_owner='',lease_token='',lease_expires_at=NULL,revision=revision+1,updated_at=?
		WHERE id=? AND lease_token=? AND status='active'
	`), status, failure.ErrorCode, failure.ErrorMessage, failure.Now, failure.ID, failure.LeaseToken)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportLeaseLost
	}
	return r.getUnscoped(ctx, failure.ID)
}

func (r *Repository) Complete(ctx context.Context, completion model.CompleteContentImport) (*model.ContentImport, error) {
	result, err := r.db.ExecContext(ctx, r.bind(`
		UPDATE content_imports SET status='completed',stage='completed',progress=100,result_note_id=?,error_code='',error_message='',
			lease_owner='',lease_token='',lease_expires_at=NULL,revision=revision+1,updated_at=?
		WHERE id=? AND lease_token=? AND status='active'
	`), completion.ResultNoteID, completion.Now, completion.ID, completion.LeaseToken)
	if err != nil {
		return nil, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return nil, storage.ErrContentImportLeaseLost
	}
	return r.getUnscoped(ctx, completion.ID)
}

func (r *Repository) getByLease(ctx context.Context, id, leaseToken string) (*model.ContentImport, error) {
	return scanContentImport(r.db.QueryRowContext(ctx, r.bind(contentImportSelect+" WHERE id=? AND lease_token=?"), id, leaseToken))
}

func (r *Repository) getUnscoped(ctx context.Context, id string) (*model.ContentImport, error) {
	return scanContentImport(r.db.QueryRowContext(ctx, r.bind(contentImportSelect+" WHERE id=?"), id))
}

const contentImportColumns = `id,source_url,source_type,canonical_url,external_id,feed_url,title,podcast_title,cover_url,description,
	duration_seconds,transcript_url,audio_url,status,stage,progress,summarize_with_ai,summary_prompt,include_transcript,language,folder_id,
	project_ids,tags,result_note_id,error_code,error_message,revision,created_at,updated_at,attempt,max_attempts`

const contentImportSelect = `SELECT ` + contentImportColumns + ` FROM content_imports`

type scanner interface {
	Scan(...interface{}) error
}

func scanContentImport(row scanner) (*model.ContentImport, error) {
	return scanContentImportWithPrefix(row)
}

func scanContentImportWithPrefix(row scanner, prefix ...interface{}) (*model.ContentImport, error) {
	var item model.ContentImport
	var projectIDs, tags string
	destinations := append(prefix,
		&item.ID, &item.SourceURL, &item.SourceType, &item.CanonicalURL, &item.ExternalID, &item.FeedURL,
		&item.Title, &item.PodcastTitle, &item.CoverURL, &item.Description, &item.DurationSeconds,
		&item.TranscriptURL, &item.AudioURL, &item.Status, &item.Stage, &item.Progress,
		&item.SummarizeWithAI, &item.SummaryPrompt, &item.IncludeTranscript, &item.Language, &item.FolderID,
		&projectIDs, &tags, &item.ResultNoteID, &item.ErrorCode, &item.ErrorMessage,
		&item.Revision, &item.CreatedAt, &item.UpdatedAt, &item.Attempt, &item.MaxAttempts,
	)
	if err := row.Scan(destinations...); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(projectIDs), &item.ProjectIDs)
	_ = json.Unmarshal([]byte(tags), &item.Tags)
	if item.ProjectIDs == nil {
		item.ProjectIDs = []string{}
	}
	if item.Tags == nil {
		item.Tags = []string{}
	}
	item.Retryable = (item.Status == model.ContentImportStatusFailed || item.Status == model.ContentImportStatusNeedsReview) && item.Attempt < item.MaxAttempts
	return &item, nil
}
