package mobilev2projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
)

func projectContent(
	ctx context.Context,
	runner Runner,
	dialect Dialect,
	projection Projection,
) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	appenders := []func(context.Context, Runner, Dialect, Projection, []json.RawMessage) ([]json.RawMessage, error){
		appendNotes,
		appendVoiceNotes,
		appendInbox,
		appendTranscriptionJobs,
	}
	var err error
	for _, appendEntities := range appenders {
		result, err = appendEntities(ctx, runner, dialect, projection, result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func appendNotes(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	tagsExpression := "tags"
	if dialect == DialectPostgres {
		tagsExpression = "to_json(tags)::text"
	}
	query := fmt.Sprintf(`SELECT
		id,client_id,revision,title,body,COALESCE(folder_id,''),%s,created_at,updated_at,deleted_at
		FROM notes WHERE workspace_id=? ORDER BY id`, tagsExpression)
	rows, err := runner.QueryContext(ctx, bind(dialect, query), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, title, body, folderID       string
			clientID                        sql.NullString
			entityRevision                  int64
			tagsRaw                         []byte
			createdAt, updatedAt, deletedAt flexibleInstant
		)
		if err := rows.Scan(
			&id, &clientID, &entityRevision, &title, &body, &folderID, &tagsRaw,
			&createdAt, &updatedAt, &deletedAt,
		); err != nil {
			return nil, err
		}
		tags, err := decodeStrings(tagsRaw)
		if err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "note", EntityID: id, ClientID: optionalClientID(clientID),
			EntityRevision:     strconv.FormatInt(entityRevision, 10),
			AggregateRevisions: aggregateRevisions{}, DeletedAt: instantString(deletedAt),
			Payload: map[string]any{
				"title": title, "body": body, "folder_id": folderID, "tags": tags,
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendVoiceNotes(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		v.id,v.client_id,v.revision,v.note_id,n.title,n.body,v.duration_ms,v.recorded_at,v.language,
		v.upload_state,v.audio_state,v.audio_revision,v.transcription_state,v.transcription_error,
		v.mime_type,v.audio_size,v.audio_sha256,v.created_at,v.updated_at,v.deleted_at
		FROM voice_notes v
		JOIN notes n ON n.workspace_id=v.workspace_id AND n.id=v.note_id
		WHERE v.workspace_id=? ORDER BY v.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, noteID, title, body, language                    string
			uploadState, audioState, transcriptionState          string
			transcriptionError, mimeType, audioSHA256            string
			clientID                                             sql.NullString
			entityRevision, durationMS, audioRevision, audioSize int64
			recordedAt, createdAt, updatedAt, deletedAt          flexibleInstant
		)
		if err := rows.Scan(
			&id, &clientID, &entityRevision, &noteID, &title, &body, &durationMS, &recordedAt, &language,
			&uploadState, &audioState, &audioRevision, &transcriptionState, &transcriptionError,
			&mimeType, &audioSize, &audioSHA256, &createdAt, &updatedAt, &deletedAt,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "voice_note", EntityID: id, ClientID: optionalClientID(clientID),
			EntityRevision:     strconv.FormatInt(entityRevision, 10),
			AggregateRevisions: aggregateRevisions{}, DeletedAt: instantString(deletedAt),
			Payload: map[string]any{
				"note_id": noteID, "title": title, "body": body,
				"duration_ms": strconv.FormatInt(durationMS, 10), "recorded_at": requiredInstantString(recordedAt),
				"language": language, "upload_state": uploadState, "audio_state": audioState,
				"audio_revision": strconv.FormatInt(audioRevision, 10), "transcription_state": transcriptionState,
				"transcription_error": transcriptionError, "mime_type": mimeType,
				"audio_size": strconv.FormatInt(audioSize, 10), "audio_sha256": audioSHA256,
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendInbox(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		id,client_id,revision,kind,title,body,archived,created_at,updated_at,deleted_at
		FROM inbox WHERE workspace_id=? ORDER BY id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, kind, title                 string
			clientID, body                  sql.NullString
			entityRevision                  int64
			archived                        bool
			createdAt, updatedAt, deletedAt flexibleInstant
		)
		if err := rows.Scan(
			&id, &clientID, &entityRevision, &kind, &title, &body, &archived,
			&createdAt, &updatedAt, &deletedAt,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "inbox", EntityID: id, ClientID: optionalClientID(clientID),
			EntityRevision:     strconv.FormatInt(entityRevision, 10),
			AggregateRevisions: aggregateRevisions{}, DeletedAt: instantString(deletedAt),
			Payload: map[string]any{
				"kind": kind, "title": title, "body": optionalString(body), "archived": archived,
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendTranscriptionJobs(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		j.job_id,v.id,j.generation,j.state,j.revision,j.error_code,j.next_attempt_at,j.created_at,j.updated_at
		FROM transcription_jobs j
		JOIN voice_notes v ON v.workspace_id=j.workspace_id AND v.client_id=j.voice_note_id
		WHERE j.workspace_id=? ORDER BY j.job_id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, voiceNoteID, state, errorCode   string
			generation, entityRevision          int64
			nextAttemptAt, createdAt, updatedAt flexibleInstant
		)
		if err := rows.Scan(
			&id, &voiceNoteID, &generation, &state, &entityRevision, &errorCode,
			&nextAttemptAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "transcription_job", EntityID: id,
			EntityRevision:     strconv.FormatInt(entityRevision, 10),
			AggregateRevisions: aggregateRevisions{},
			Payload: map[string]any{
				"voice_note_id": voiceNoteID, "generation": strconv.FormatInt(generation, 10),
				"state": state, "error_code": errorCode, "next_attempt_at": instantString(nextAttemptAt),
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}
