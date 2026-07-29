package mobilev2service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
)

type noteCommandPayload struct {
	Title    *string   `json:"title"`
	Body     *string   `json:"body"`
	FolderID *string   `json:"folder_id"`
	Tags     *[]string `json:"tags"`
}

type inboxCommandPayload struct {
	Kind     *string             `json:"kind"`
	Title    *string             `json:"title"`
	Body     nullablePatchString `json:"body"`
	Archived *bool               `json:"archived"`
}

type voiceCreateCommandPayload struct {
	Title      string `json:"title"`
	DurationMS string `json:"duration_ms"`
	RecordedAt string `json:"recorded_at"`
	Language   string `json:"language"`
}

type transcriptionCommandPayload struct {
	Language    string  `json:"language"`
	FailedJobID *string `json:"failed_job_id"`
}

type contentCommandOutcome struct {
	Result    mobilev2command.DomainResult
	EntityIDs map[string]map[string]struct{}
}

func newContentCommandOutcome() contentCommandOutcome {
	return contentCommandOutcome{EntityIDs: make(map[string]map[string]struct{})}
}

func (outcome *contentCommandOutcome) include(entityType, entityID string) {
	if strings.TrimSpace(entityType) == "" || strings.TrimSpace(entityID) == "" {
		return
	}
	ids := outcome.EntityIDs[entityType]
	if ids == nil {
		ids = make(map[string]struct{})
		outcome.EntityIDs[entityType] = ids
	}
	ids[entityID] = struct{}{}
}

func (executor *CommandExecutor) applyContentCommand(
	ctx context.Context,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
) (any, error) {
	runtime, err := executor.runtime.ResolveMobileRuntime(ctx, identity.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if runtime.WorkspaceID != identity.WorkspaceID || runtime.Epoch < 1 || runtime.DB == nil || runtime.Writer == nil {
		return nil, errors.New("incomplete mobile-v2 content runtime")
	}
	writer, ok := runtime.Writer.(storage.MobileV2TenantFencedWriter)
	if !ok {
		return nil, errors.New("tenant writer does not expose a mobile-v2 content transaction")
	}
	ledger, dialect, err := commandLedger(runtime)
	if err != nil {
		return nil, err
	}
	command := envelope.LedgerCommand()
	var response mobilev2command.Response
	err = writer.BeginFencedMobileV2Write(ctx, runtime.WorkspaceID, runtime.Epoch, func(tx storage.MobileV2TenantWriteTx) error {
		runner := tx.MobileV2SQLRunner()
		if runner == nil {
			return errors.New("tenant transaction does not expose a mobile-v2 SQL runner")
		}
		prepared, proceed, err := ledger.PrepareOnRunner(ctx, runner, command)
		if err != nil {
			return err
		}
		if !proceed {
			response = prepared
			return nil
		}
		envelope, dependencyState, err := resolveCommandDependencies(ctx, ledger, runner, envelope)
		if err != nil {
			return err
		}
		if dependencyState == dependencyPending {
			response = mobilev2command.Response{RetryLater: true}
			return nil
		}
		if dependencyState == dependencyRejected {
			response, err = ledger.FinalizeOnRunner(ctx, runner, command, executor.now().UTC(),
				mobilev2command.DynamicCommitResult{
					DomainResult: mobilev2command.DomainResult{Status: mobilev2command.StatusRejected},
				})
			return err
		}
		if envelope.CreatedRuntimeEpoch != strconv.FormatInt(runtime.Epoch, 10) {
			return mobilev2command.ErrStaleRuntimeEpoch
		}
		outcome, err := dispatchContentCommand(ctx, runner, dialect, identity, envelope, executor.now().UTC())
		if err != nil {
			return err
		}
		currentSequence, err := ledger.CurrentSequenceOnRunner(ctx, runner, runtime.WorkspaceID)
		if err != nil {
			return err
		}
		scopeChange, afterImages, err := projectContentCommandOutcome(
			ctx, runner, dialect, runtime.WorkspaceID, currentSequence+1, outcome,
		)
		if err != nil {
			return err
		}
		outcome.Result.AfterImages = afterImages
		response, err = ledger.FinalizeOnRunner(ctx, runner, command, executor.now().UTC(),
			mobilev2command.DynamicCommitResult{
				DomainResult: outcome.Result,
				ScopeChanges: []mobilev2command.ScopeChange{scopeChange},
			})
		return err
	})
	if err != nil {
		return nil, commandProtocolError(err)
	}
	return commandResponse(response)
}

func dispatchContentCommand(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
	now time.Time,
) (contentCommandOutcome, error) {
	switch {
	case strings.HasPrefix(envelope.CommandType, "note."):
		return dispatchNoteCommand(ctx, runner, dialect, identity.WorkspaceID, envelope, now)
	case strings.HasPrefix(envelope.CommandType, "inbox."):
		return dispatchInboxCommand(ctx, runner, dialect, identity.WorkspaceID, envelope, now)
	case strings.HasPrefix(envelope.CommandType, "voice"):
		return dispatchVoiceCommand(ctx, runner, dialect, identity.WorkspaceID, envelope, now)
	case strings.HasPrefix(envelope.CommandType, "transcription."):
		return dispatchTranscriptionCommand(ctx, runner, dialect, identity.WorkspaceID, envelope, now)
	default:
		return newContentCommandOutcome(), mobilev2command.ErrInvalidCommandEnvelope
	}
}

func dispatchNoteCommand(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	envelope mobilev2command.Envelope,
	now time.Time,
) (contentCommandOutcome, error) {
	outcome := newContentCommandOutcome()
	var payload noteCommandPayload
	if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
		return outcome, err
	}
	if envelope.CommandType == "note.create" {
		if envelope.Target.ClientID == nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		entityID := uuid.NewString()
		title, body := optionalPatchValue(payload.Title), optionalPatchValue(payload.Body)
		folderID, err := contentFolderValue(ctx, runner, dialect, workspaceID, payload.FolderID)
		if err != nil {
			return outcome, err
		}
		tagsJSON, err := normalizedTagsJSON(payload.Tags)
		if err != nil {
			return outcome, err
		}
		query := `INSERT INTO notes
			(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at)
			VALUES (?,?,?,1,?,?,?,?,?,?)`
		if dialect == mobilev2projection.DialectPostgres {
			query = `INSERT INTO notes
				(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at)
				VALUES (?,?,?,1,?,?,?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?)`
		}
		timestamp := contentTimestamp(dialect, now)
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, query),
			entityID, workspaceID, *envelope.Target.ClientID, title, body, folderID, tagsJSON, timestamp, timestamp); err != nil {
			return outcome, err
		}
		outcome.include("note", entityID)
		clientID := *envelope.Target.ClientID
		outcome.Result.Status = mobilev2command.StatusApplied
		outcome.Result.IdentityMappings = []mobilev2command.IdentityMapping{{
			EntityType: "note", ClientID: &clientID, EntityID: &entityID,
		}}
		outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
			EntityType: "note", EntityID: entityID, Revision: "1",
		}}
		return outcome, nil
	}

	entityID := targetEntityID(envelope.Target)
	if entityID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.include("note", entityID)
	expected, err := envelope.Expected.Exact("entity")
	if err != nil {
		return outcome, err
	}
	current, deleted, found, err := contentEntityState(ctx, runner, dialect, "notes", "id", workspaceID, entityID)
	if err != nil {
		return outcome, err
	}
	if !found || deleted || current != expected {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	timestamp := contentTimestamp(dialect, now)
	switch envelope.CommandType {
	case "note.update":
		sets := []string{"revision=revision+1", "updated_at=?"}
		args := []any{timestamp}
		if payload.Title != nil {
			sets, args = append(sets, "title=?"), append(args, *payload.Title)
		}
		if payload.Body != nil {
			sets, args = append(sets, "body=?"), append(args, *payload.Body)
		}
		if payload.FolderID != nil {
			folderID, err := contentFolderValue(ctx, runner, dialect, workspaceID, payload.FolderID)
			if err != nil {
				return outcome, err
			}
			sets, args = append(sets, "folder_id=?"), append(args, folderID)
		}
		if payload.Tags != nil {
			tagsJSON, err := normalizedTagsJSON(payload.Tags)
			if err != nil {
				return outcome, err
			}
			tagSet := "tags=?"
			if dialect == mobilev2projection.DialectPostgres {
				tagSet = "tags=ARRAY(SELECT jsonb_array_elements_text(?::jsonb))"
			}
			sets, args = append(sets, tagSet), append(args, tagsJSON)
		}
		args = append(args, workspaceID, entityID, expected)
		result, err := runner.ExecContext(ctx, bindContentSQL(dialect,
			`UPDATE notes SET `+strings.Join(sets, ",")+`
			 WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`), args...)
		if err != nil {
			return outcome, err
		}
		if !exactlyOneRow(result) {
			outcome.Result.Status = mobilev2command.StatusConflict
			return outcome, nil
		}
	case "note.delete":
		if err := decodeEmptyPayload(envelope.Payload); err != nil {
			return outcome, err
		}
		result, err := runner.ExecContext(ctx, bindContentSQL(dialect, `UPDATE notes
			SET deleted_at=?,updated_at=?,revision=revision+1
			WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`),
			timestamp, timestamp, workspaceID, entityID, expected)
		if err != nil {
			return outcome, err
		}
		if !exactlyOneRow(result) {
			outcome.Result.Status = mobilev2command.StatusConflict
			return outcome, nil
		}
	default:
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.Result.Status = mobilev2command.StatusApplied
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
		EntityType: "note", EntityID: entityID, Revision: strconv.FormatInt(expected+1, 10),
	}}
	return outcome, nil
}

func dispatchInboxCommand(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	envelope mobilev2command.Envelope,
	now time.Time,
) (contentCommandOutcome, error) {
	outcome := newContentCommandOutcome()
	var payload inboxCommandPayload
	if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
		return outcome, err
	}
	if envelope.CommandType == "inbox.create" {
		if envelope.Target.ClientID == nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		entityID := uuid.NewString()
		kind := "note"
		if payload.Kind != nil && strings.TrimSpace(*payload.Kind) != "" {
			kind = strings.TrimSpace(*payload.Kind)
		}
		title := optionalPatchValue(payload.Title)
		var body any
		if payload.Body.Set {
			body = payload.Body.Value
		}
		archived := payload.Archived != nil && *payload.Archived
		timestamp := contentTimestamp(dialect, now)
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `INSERT INTO inbox
			(id,workspace_id,client_id,revision,kind,title,body,archived,created_at,updated_at)
			VALUES (?,?,?,1,?,?,?,?,?,?)`),
			entityID, workspaceID, *envelope.Target.ClientID, kind, title, nullableStringArgument(body), archived, timestamp, timestamp); err != nil {
			return outcome, err
		}
		outcome.include("inbox", entityID)
		clientID := *envelope.Target.ClientID
		outcome.Result.Status = mobilev2command.StatusApplied
		outcome.Result.IdentityMappings = []mobilev2command.IdentityMapping{{
			EntityType: "inbox", ClientID: &clientID, EntityID: &entityID,
		}}
		outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
			EntityType: "inbox", EntityID: entityID, Revision: "1",
		}}
		return outcome, nil
	}

	entityID := targetEntityID(envelope.Target)
	if entityID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.include("inbox", entityID)
	expected, err := envelope.Expected.Exact("entity")
	if err != nil {
		return outcome, err
	}
	current, deleted, found, err := contentEntityState(ctx, runner, dialect, "inbox", "id", workspaceID, entityID)
	if err != nil {
		return outcome, err
	}
	if !found || deleted || current != expected {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	timestamp := contentTimestamp(dialect, now)
	switch envelope.CommandType {
	case "inbox.update":
		sets := []string{"revision=revision+1", "updated_at=?"}
		args := []any{timestamp}
		if payload.Kind != nil {
			sets, args = append(sets, "kind=?"), append(args, strings.TrimSpace(*payload.Kind))
		}
		if payload.Title != nil {
			sets, args = append(sets, "title=?"), append(args, *payload.Title)
		}
		if payload.Body.Set {
			sets, args = append(sets, "body=?"), append(args, nullableStringArgument(payload.Body.Value))
		}
		if payload.Archived != nil {
			sets, args = append(sets, "archived=?"), append(args, *payload.Archived)
		}
		args = append(args, workspaceID, entityID, expected)
		result, err := runner.ExecContext(ctx, bindContentSQL(dialect,
			`UPDATE inbox SET `+strings.Join(sets, ",")+`
			 WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`), args...)
		if err != nil {
			return outcome, err
		}
		if !exactlyOneRow(result) {
			outcome.Result.Status = mobilev2command.StatusConflict
			return outcome, nil
		}
	case "inbox.delete":
		if err := decodeEmptyPayload(envelope.Payload); err != nil {
			return outcome, err
		}
		result, err := runner.ExecContext(ctx, bindContentSQL(dialect, `UPDATE inbox
			SET deleted_at=?,updated_at=?,revision=revision+1
			WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`),
			timestamp, timestamp, workspaceID, entityID, expected)
		if err != nil {
			return outcome, err
		}
		if !exactlyOneRow(result) {
			outcome.Result.Status = mobilev2command.StatusConflict
			return outcome, nil
		}
	default:
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.Result.Status = mobilev2command.StatusApplied
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{{
		EntityType: "inbox", EntityID: entityID, Revision: strconv.FormatInt(expected+1, 10),
	}}
	return outcome, nil
}

func dispatchVoiceCommand(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	envelope mobilev2command.Envelope,
	now time.Time,
) (contentCommandOutcome, error) {
	outcome := newContentCommandOutcome()
	if envelope.CommandType == "voice.create" {
		if envelope.Target.ClientID == nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		var payload voiceCreateCommandPayload
		if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
			return outcome, err
		}
		duration, err := strconv.ParseInt(payload.DurationMS, 10, 64)
		if err != nil || duration < 0 {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		recordedAt, err := time.Parse("2006-01-02T15:04:05.000Z", payload.RecordedAt)
		if err != nil {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			title = "Voice note"
		}
		noteID, noteClientID, voiceID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		folderID, err := contentFolderValue(ctx, runner, dialect, workspaceID, nil)
		if err != nil {
			return outcome, err
		}
		tagsJSON := `["voice"]`
		query := `INSERT INTO notes
			(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at)
			VALUES (?,?,?,1,?,'',?,?,?,?)`
		if dialect == mobilev2projection.DialectPostgres {
			query = `INSERT INTO notes
				(id,workspace_id,client_id,revision,title,body,folder_id,tags,created_at,updated_at)
				VALUES (?,?,?,1,?,'',?,ARRAY(SELECT jsonb_array_elements_text(?::jsonb)),?,?)`
		}
		timestamp := contentTimestamp(dialect, now)
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, query),
			noteID, workspaceID, noteClientID, title, folderID, tagsJSON, timestamp, timestamp); err != nil {
			return outcome, err
		}
		unixNow := now.Unix()
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `INSERT INTO voice_notes
			(id,workspace_id,client_id,revision,audio_revision,audio_state,note_id,duration_ms,recorded_at,language,
			 object_key,mime_type,audio_size,audio_sha256,upload_state,transcription_state,transcription_error,created_at,updated_at)
			VALUES (?,?,?,1,1,'absent',?,?,?,?, '','',0,'','pending','not_started','',?,?)`),
			voiceID, workspaceID, *envelope.Target.ClientID, noteID, duration, recordedAt.UTC().Unix(),
			strings.TrimSpace(payload.Language), unixNow, unixNow); err != nil {
			return outcome, err
		}
		outcome.include("note", noteID)
		outcome.include("voice_note", voiceID)
		clientID := *envelope.Target.ClientID
		outcome.Result.Status = mobilev2command.StatusApplied
		outcome.Result.IdentityMappings = []mobilev2command.IdentityMapping{
			{EntityType: "voice_note", ClientID: &clientID, EntityID: &voiceID},
			{EntityType: "note", ClientID: &noteClientID, EntityID: &noteID},
		}
		outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{
			{EntityType: "voice_note", EntityID: voiceID, Revision: "1"},
			{EntityType: "note", EntityID: noteID, Revision: "1"},
		}
		return outcome, nil
	}

	if err := decodeEmptyPayload(envelope.Payload); err != nil {
		return outcome, err
	}
	voiceID := targetEntityID(envelope.Target)
	if voiceID == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.include("voice_note", voiceID)
	expected, err := envelope.Expected.Exact("entity")
	if err != nil {
		return outcome, err
	}
	var current int64
	var noteID, clientID, objectKey string
	var deleted bool
	err = runner.QueryRowContext(ctx, bindContentSQL(dialect, `SELECT
		revision,note_id,client_id,object_key,(deleted_at IS NOT NULL)
		FROM voice_notes WHERE workspace_id=? AND id=?`), workspaceID, voiceID).
		Scan(&current, &noteID, &clientID, &objectKey, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted || current != expected {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	if err != nil {
		return outcome, err
	}
	unixNow := now.Unix()
	audioState := "deleted"
	if objectKey != "" {
		audioState = "delete_requested"
	}
	setDeleted := ""
	args := []any{audioState, objectKey, objectKey, objectKey, objectKey, objectKey}
	if envelope.CommandType == "voice_note.delete" {
		setDeleted = "deleted_at=?,"
		args = append([]any{contentTimestamp(dialect, now)}, args...)
	} else if envelope.CommandType != "voice_audio.delete" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	args = append(args, unixNow, workspaceID, voiceID, expected)
	result, err := runner.ExecContext(ctx, bindContentSQL(dialect, `UPDATE voice_notes SET `+setDeleted+`
		audio_state=?,audio_revision=audio_revision+1,
		object_key=CASE WHEN ?='' THEN '' ELSE object_key END,
		mime_type=CASE WHEN ?='' THEN '' ELSE mime_type END,
		audio_size=CASE WHEN ?='' THEN 0 ELSE audio_size END,
		audio_sha256=CASE WHEN ?='' THEN '' ELSE audio_sha256 END,
		upload_state=CASE WHEN ?='' THEN 'failed' ELSE upload_state END,
		revision=revision+1,updated_at=?
		WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`), args...)
	if err != nil {
		return outcome, err
	}
	if !exactlyOneRow(result) {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `UPDATE transcription_jobs
		SET state='canceled',revision=revision+1,error_code='voice_audio_deleted',
		    next_attempt_at=NULL,lease_owner='',lease_token='',lease_expires_at=NULL,heartbeat_at=NULL,updated_at=?
		WHERE workspace_id=? AND voice_note_id=?
		  AND state IN ('waiting_for_audio','queued','processing','retry_waiting')`),
		unixNow, workspaceID, clientID); err != nil {
		return outcome, err
	}
	if objectKey != "" {
		insert := `INSERT OR IGNORE INTO voice_audio_cleanup_jobs
			(job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,next_attempt_at,lease_owner,lease_token,created_at,updated_at)
			VALUES (?,?,?,?,'retry_waiting',1,0,6,'',?,'','',?,?)`
		if dialect == mobilev2projection.DialectPostgres {
			insert = `INSERT INTO voice_audio_cleanup_jobs
				(job_id,workspace_id,voice_note_id,object_key,state,revision,attempt,max_attempts,error_code,next_attempt_at,lease_owner,lease_token,created_at,updated_at)
				VALUES (?,?,?,?,'retry_waiting',1,0,6,'',?,'','',?,?)
				ON CONFLICT (workspace_id,voice_note_id,object_key) DO NOTHING`
		}
		if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, insert),
			uuid.NewString(), workspaceID, clientID, objectKey, unixNow+600, unixNow, unixNow); err != nil {
			return outcome, err
		}
	}
	revisions := []mobilev2command.AffectedRevision{{
		EntityType: "voice_note", EntityID: voiceID, Revision: strconv.FormatInt(expected+1, 10),
	}}
	if envelope.CommandType == "voice_note.delete" {
		outcome.include("note", noteID)
		noteTimestamp := contentTimestamp(dialect, now)
		var noteRevision int64
		err := runner.QueryRowContext(ctx, bindContentSQL(dialect, `UPDATE notes
			SET deleted_at=?,updated_at=?,revision=revision+1
			WHERE workspace_id=? AND id=? AND deleted_at IS NULL
			RETURNING revision`), noteTimestamp, noteTimestamp, workspaceID, noteID).Scan(&noteRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return outcome, err
		}
		if err == nil {
			revisions = append(revisions, mobilev2command.AffectedRevision{
				EntityType: "note", EntityID: noteID, Revision: strconv.FormatInt(noteRevision, 10),
			})
		}
	}
	outcome.Result.Status = mobilev2command.StatusApplied
	outcome.Result.AffectedRevisions = revisions
	return outcome, nil
}

func dispatchTranscriptionCommand(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	envelope mobilev2command.Envelope,
	now time.Time,
) (contentCommandOutcome, error) {
	outcome := newContentCommandOutcome()
	var payload transcriptionCommandPayload
	if err := decodeCommandPayload(envelope.Payload, &payload); err != nil {
		return outcome, err
	}
	voiceID := targetEntityID(envelope.Target)
	if voiceID == "" || strings.TrimSpace(payload.Language) == "" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	outcome.include("voice_note", voiceID)
	expected, err := envelope.Expected.Exact("entity")
	if err != nil {
		return outcome, err
	}
	var current int64
	var clientID, uploadState, audioState string
	var deleted bool
	err = runner.QueryRowContext(ctx, bindContentSQL(dialect, `SELECT
		revision,client_id,upload_state,audio_state,(deleted_at IS NOT NULL)
		FROM voice_notes WHERE workspace_id=? AND id=?`), workspaceID, voiceID).
		Scan(&current, &clientID, &uploadState, &audioState, &deleted)
	if errors.Is(err, sql.ErrNoRows) || deleted || current != expected {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	if err != nil {
		return outcome, err
	}
	if audioState == "delete_requested" || audioState == "deleted" {
		outcome.Result.Status = mobilev2command.StatusRejected
		return outcome, nil
	}
	var generation int64
	language := strings.TrimSpace(payload.Language)
	if envelope.CommandType == "transcription.retry" {
		if payload.FailedJobID == nil || strings.TrimSpace(*payload.FailedJobID) == "" {
			return outcome, mobilev2command.ErrInvalidCommandEnvelope
		}
		var failedVoiceClientID, failedState, failedLanguage string
		err := runner.QueryRowContext(ctx, bindContentSQL(dialect, `SELECT voice_note_id,state,language
			FROM transcription_jobs WHERE workspace_id=? AND job_id=?`),
			workspaceID, *payload.FailedJobID).Scan(&failedVoiceClientID, &failedState, &failedLanguage)
		if errors.Is(err, sql.ErrNoRows) || failedVoiceClientID != clientID || failedState != "failed" {
			outcome.Result.Status = mobilev2command.StatusRejected
			return outcome, nil
		}
		if err != nil {
			return outcome, err
		}
		if language == "" {
			language = failedLanguage
		}
	} else if envelope.CommandType != "transcription.request" {
		return outcome, mobilev2command.ErrInvalidCommandEnvelope
	}
	if err := runner.QueryRowContext(ctx, bindContentSQL(dialect, `SELECT
		COALESCE(MAX(generation),0)+1 FROM transcription_jobs
		WHERE workspace_id=? AND voice_note_id=?`), workspaceID, clientID).Scan(&generation); err != nil {
		return outcome, err
	}
	state := "waiting_for_audio"
	if uploadState == "uploaded" {
		state = "queued"
	}
	jobID, unixNow := uuid.NewString(), now.Unix()
	if _, err := runner.ExecContext(ctx, bindContentSQL(dialect, `INSERT INTO transcription_jobs
		(job_id,workspace_id,voice_note_id,generation,state,revision,language,attempt,error_code,next_attempt_at,created_at,updated_at)
		VALUES (?,?,?,?,?,1,?,0,'',NULL,?,?)`),
		jobID, workspaceID, clientID, generation, state, language, unixNow, unixNow); err != nil {
		return outcome, err
	}
	result, err := runner.ExecContext(ctx, bindContentSQL(dialect, `UPDATE voice_notes
		SET transcription_state='processing',transcription_error='',revision=revision+1,updated_at=?
		WHERE workspace_id=? AND id=? AND revision=? AND deleted_at IS NULL`),
		unixNow, workspaceID, voiceID, expected)
	if err != nil {
		return outcome, err
	}
	if !exactlyOneRow(result) {
		outcome.Result.Status = mobilev2command.StatusConflict
		return outcome, nil
	}
	outcome.include("transcription_job", jobID)
	outcome.Result.Status = mobilev2command.StatusApplied
	outcome.Result.AffectedRevisions = []mobilev2command.AffectedRevision{
		{EntityType: "voice_note", EntityID: voiceID, Revision: strconv.FormatInt(expected+1, 10)},
		{EntityType: "transcription_job", EntityID: jobID, Revision: "1"},
	}
	return outcome, nil
}

func projectContentCommandOutcome(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	sequence uint64,
	outcome contentCommandOutcome,
) (mobilev2command.ScopeChange, [][]byte, error) {
	projected, err := mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
		WorkspaceID: workspaceID,
		Scope:       mobilev2sync.ScopeIPhoneContent,
		AsOf:        time.Now().UTC(),
		Sequence:    sequence,
	})
	if err != nil {
		return mobilev2command.ScopeChange{}, nil, err
	}
	filtered := make([][]byte, 0)
	for _, image := range projected {
		var header struct {
			EntityType string `json:"entity_type"`
			EntityID   string `json:"entity_id"`
		}
		if err := json.Unmarshal(image, &header); err != nil {
			return mobilev2command.ScopeChange{}, nil, err
		}
		if _, include := outcome.EntityIDs[header.EntityType][header.EntityID]; include {
			filtered = append(filtered, append([]byte(nil), image...))
		}
	}
	return mobilev2command.ScopeChange{
		Scope: mobilev2sync.ScopeIPhoneContent, AfterImages: filtered,
	}, filtered, nil
}

func contentEntityState(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	table, idColumn, workspaceID, entityID string,
) (revision int64, deleted, found bool, err error) {
	query := fmt.Sprintf(`SELECT revision,(deleted_at IS NOT NULL) FROM %s
		WHERE workspace_id=? AND %s=?`, table, idColumn)
	err = runner.QueryRowContext(ctx, bindContentSQL(dialect, query), workspaceID, entityID).
		Scan(&revision, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, false, nil
	}
	return revision, deleted, err == nil, err
}

func contentFolderValue(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	requested *string,
) (any, error) {
	if requested != nil && strings.TrimSpace(*requested) != "" {
		return strings.TrimSpace(*requested), nil
	}
	var folderID string
	err := runner.QueryRowContext(ctx, bindContentSQL(dialect, `SELECT id FROM folders
		WHERE workspace_id=? ORDER BY CASE WHEN id='__uncategorized' THEN 0 ELSE 1 END,id LIMIT 1`),
		workspaceID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return folderID, err
}

func normalizedTagsJSON(tags *[]string) (string, error) {
	if tags == nil {
		return "[]", nil
	}
	seen := make(map[string]struct{}, len(*tags))
	normalized := make([]string, 0, len(*tags))
	for _, tag := range *tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return "", mobilev2command.ErrInvalidCommandEnvelope
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	encoded, err := json.Marshal(normalized)
	return string(encoded), err
}

func optionalPatchValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableStringArgument(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case *string:
		if typed == nil {
			return nil
		}
		return *typed
	default:
		return typed
	}
}

func contentTimestamp(dialect mobilev2projection.Dialect, now time.Time) any {
	if dialect == mobilev2projection.DialectSQLite {
		return now.UTC().Unix()
	}
	return now.UTC()
}

func bindContentSQL(dialect mobilev2projection.Dialect, query string) string {
	if dialect != mobilev2projection.DialectPostgres {
		return query
	}
	var builder strings.Builder
	builder.Grow(len(query) + 16)
	index := 1
	for _, character := range query {
		if character == '?' {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(index))
			index++
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func exactlyOneRow(result sql.Result) bool {
	if result == nil {
		return false
	}
	affected, err := result.RowsAffected()
	return err == nil && affected == 1
}
