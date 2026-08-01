package mobilev2change

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
)

type Runner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// LockCommitHead serializes ordinary web/task writes with mobile command
// receipts. The caller keeps this transaction open through AppendTaskChanges.
func LockCommitHead(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
) (uint64, error) {
	if runner == nil || strings.TrimSpace(workspaceID) == "" {
		return 0, errors.New("invalid mobile-v2 task change head")
	}
	if dialect == mobilev2projection.DialectPostgres {
		if _, err := runner.ExecContext(ctx, `INSERT INTO mobile_v2_commit_heads(workspace_id)
			VALUES ($1) ON CONFLICT DO NOTHING`, workspaceID); err != nil {
			return 0, err
		}
		var sequence uint64
		err := runner.QueryRowContext(ctx, `SELECT latest_sequence FROM mobile_v2_commit_heads
			WHERE workspace_id=$1 FOR UPDATE`, workspaceID).Scan(&sequence)
		return sequence, err
	}
	if _, err := runner.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_v2_commit_heads(workspace_id)
		VALUES (?)`, workspaceID); err != nil {
		return 0, err
	}
	if _, err := runner.ExecContext(ctx, `UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence WHERE workspace_id=?`, workspaceID); err != nil {
		return 0, err
	}
	var sequence uint64
	err := runner.QueryRowContext(ctx, `SELECT latest_sequence FROM mobile_v2_commit_heads
		WHERE workspace_id=?`, workspaceID).Scan(&sequence)
	return sequence, err
}

// AppendTaskChanges publishes server-originated task changes. If the callback
// already advanced the head, it was a mobile command and its terminal receipt
// already owns the change batches, so no duplicate server batch is emitted.
func AppendTaskChanges(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	initialSequence uint64,
	changes storage.MobileV2TaskChangeSnapshot,
	now time.Time,
) error {
	if changes.Empty() {
		return nil
	}
	var current uint64
	if err := runner.QueryRowContext(ctx, bind(dialect,
		`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=?`), workspaceID).Scan(&current); err != nil {
		return err
	}
	if current != initialSequence {
		return nil
	}
	var sequence uint64
	if err := runner.QueryRowContext(ctx, bind(dialect, `UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? RETURNING latest_sequence`), workspaceID).Scan(&sequence); err != nil {
		return err
	}
	return appendTaskChangesAtSequence(ctx, runner, dialect, workspaceID, sequence, changes, now)
}

// AppendTaskChangesAtCurrentSequence adds task scopes to a server-originated
// content change that already advanced the workspace head in the same
// transaction. It is used when deleting a note also detaches task references.
func AppendTaskChangesAtCurrentSequence(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	changes storage.MobileV2TaskChangeSnapshot,
	now time.Time,
) error {
	if changes.Empty() {
		return nil
	}
	ready, err := contentChangeSchemaReady(ctx, runner, dialect, workspaceID)
	if err != nil || !ready {
		return err
	}
	var sequence uint64
	if err := runner.QueryRowContext(ctx, bind(dialect,
		`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=?`), workspaceID).Scan(&sequence); err != nil {
		return err
	}
	if sequence < 1 {
		return errors.New("mobile-v2 content change did not reserve a sequence")
	}
	return appendTaskChangesAtSequence(ctx, runner, dialect, workspaceID, sequence, changes, now)
}

func appendTaskChangesAtSequence(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	sequence uint64,
	changes storage.MobileV2TaskChangeSnapshot,
	now time.Time,
) error {
	now = now.UTC().Truncate(time.Millisecond)
	taskCoreTombstones, occurrenceTombstones, err := deletedTaskImages(changes.Deleted, now)
	if err != nil {
		return err
	}
	if changes.FullTaskCore || len(changes.ProjectIDs) > 0 || len(changes.TaskIDs) > 0 ||
		len(taskCoreTombstones) > 0 {
		entities, err := mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
			WorkspaceID: workspaceID, Scope: mobilev2sync.ScopeIPhoneTaskCore,
			AsOf: now, Sequence: sequence,
		})
		if err != nil {
			return err
		}
		if !changes.FullTaskCore {
			entities, err = filterTaskCore(entities, changes)
			if err != nil {
				return err
			}
		}
		entities = append(entities, taskCoreTombstones...)
		if err := insertScopeBatch(ctx, runner, dialect, workspaceID,
			mobilev2sync.ScopeIPhoneTaskCore, sequence, entities, now); err != nil {
			return err
		}
	}
	if len(changes.OccurrenceIDs) > 0 || len(occurrenceTombstones) > 0 {
		entities, err := mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
			WorkspaceID: workspaceID, Scope: mobilev2sync.ScopeIPhoneOccurrenceWindow,
			AsOf: now, Sequence: sequence,
			WindowStart:     time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			WindowEnd:       time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC),
			WindowStartDate: "1970-01-01", WindowEndDate: "9999-12-31",
		})
		if err != nil {
			return err
		}
		entities, err = filterOccurrences(entities, changes.OccurrenceIDs)
		if err != nil {
			return err
		}
		entities = append(entities, occurrenceTombstones...)
		for _, scope := range []mobilev2sync.ScopeName{
			mobilev2sync.ScopeIPhoneOccurrenceWindow,
			mobilev2sync.ScopeWatchOccurrenceWindow,
		} {
			if err := insertScopeBatch(ctx, runner, dialect, workspaceID, scope, sequence, entities, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func deletedTaskImages(
	deleted []storage.MobileV2DeletedEntity,
	at time.Time,
) (taskCore []json.RawMessage, occurrences []json.RawMessage, err error) {
	for _, entity := range deleted {
		image, imageErr := mobilev2projection.Tombstone(
			entity.EntityType, entity.EntityID, entity.Revision, at,
		)
		if imageErr != nil {
			return nil, nil, imageErr
		}
		if entity.EntityType == "task_occurrence" {
			occurrences = append(occurrences, image)
		} else {
			taskCore = append(taskCore, image)
		}
	}
	return taskCore, occurrences, nil
}

func filterTaskCore(
	entities []json.RawMessage,
	changes storage.MobileV2TaskChangeSnapshot,
) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	for _, entity := range entities {
		var header struct {
			EntityType string `json:"entity_type"`
			EntityID   string `json:"entity_id"`
			Payload    struct {
				ProjectID string `json:"project_id"`
				TaskID    string `json:"task_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(entity, &header); err != nil {
			return nil, err
		}
		include := false
		switch header.EntityType {
		case "project":
			_, include = changes.ProjectIDs[header.EntityID]
		case "task", "task_schedule":
			_, include = changes.TaskIDs[header.EntityID]
		case "schedule_version":
			_, include = changes.TaskIDs[header.Payload.TaskID]
		case "roadmap", "roadmap_node", "roadmap_edge", "roadmap_node_progress":
			_, include = changes.ProjectIDs[header.Payload.ProjectID]
		}
		if include {
			result = append(result, append(json.RawMessage(nil), entity...))
		}
	}
	return result, nil
}

func filterOccurrences(entities []json.RawMessage, ids map[string]struct{}) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	for _, entity := range entities {
		var header struct {
			EntityID string `json:"entity_id"`
		}
		if err := json.Unmarshal(entity, &header); err != nil {
			return nil, err
		}
		if _, include := ids[header.EntityID]; include {
			result = append(result, append(json.RawMessage(nil), entity...))
		}
	}
	return result, nil
}

func insertScopeBatch(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	scope mobilev2sync.ScopeName,
	sequence uint64,
	entities []json.RawMessage,
	now time.Time,
) error {
	encoded, err := json.Marshal(entities)
	if err != nil {
		return err
	}
	_, err = runner.ExecContext(ctx, bind(dialect, `INSERT INTO mobile_v2_scope_change_batches
		(workspace_id,scope,sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json,committed_at)
		VALUES (?,?,?,NULL,NULL,NULL,?,?)`),
		workspaceID, scope, sequence, string(encoded), now)
	return err
}

func bind(dialect mobilev2projection.Dialect, query string) string {
	if dialect != mobilev2projection.DialectPostgres {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, character := range query {
		if character == '?' {
			builder.WriteByte('$')
			builder.WriteString(strconv.Itoa(index))
			index++
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}
