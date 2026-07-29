package mobilev2change

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
)

type ContentEntityRef struct {
	EntityType string
	EntityID   string
	ClientID   string
}

// AppendContentChanges adds a server-originated iphone-content batch to the
// same transaction as an existing web/v1 content write. Legacy databases that
// have not adopted the mobile-v2 tenant migrations are intentionally skipped.
func AppendContentChanges(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	refs []ContentEntityRef,
	now time.Time,
) error {
	if runner == nil || strings.TrimSpace(workspaceID) == "" || len(refs) == 0 {
		return nil
	}
	ready, err := contentChangeSchemaReady(ctx, runner, dialect, workspaceID)
	if err != nil || !ready {
		return err
	}
	if _, err := LockCommitHead(ctx, runner, dialect, workspaceID); err != nil {
		return err
	}
	var sequence uint64
	if err := runner.QueryRowContext(ctx, bind(dialect, `UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? RETURNING latest_sequence`), workspaceID).Scan(&sequence); err != nil {
		return err
	}
	now = now.UTC().Truncate(time.Millisecond)
	projected, err := mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
		WorkspaceID: workspaceID, Scope: mobilev2sync.ScopeIPhoneContent,
		AsOf: now, Sequence: sequence,
	})
	if err != nil {
		return err
	}
	entities, err := filterContent(projected, refs)
	if err != nil {
		return err
	}
	return insertScopeBatch(ctx, runner, dialect, workspaceID,
		mobilev2sync.ScopeIPhoneContent, sequence, entities, now)
}

func contentChangeSchemaReady(
	ctx context.Context,
	runner Runner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
) (bool, error) {
	var tableReady, workspaceReady bool
	if dialect == mobilev2projection.DialectPostgres {
		if err := runner.QueryRowContext(ctx,
			`SELECT to_regclass('mobile_v2_commit_heads') IS NOT NULL`).Scan(&tableReady); err != nil {
			return false, err
		}
		if !tableReady {
			return false, nil
		}
		err := runner.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM tenant_workspaces WHERE workspace_id=$1)`, workspaceID).
			Scan(&workspaceReady)
		return workspaceReady, err
	}
	var count int
	if err := runner.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='mobile_v2_commit_heads'`).Scan(&count); err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}
	err := runner.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM tenant_workspaces WHERE workspace_id=?)`, workspaceID).
		Scan(&workspaceReady)
	return workspaceReady, err
}

func filterContent(entities []json.RawMessage, refs []ContentEntityRef) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0, len(refs))
	for _, entity := range entities {
		var header struct {
			EntityType string  `json:"entity_type"`
			EntityID   string  `json:"entity_id"`
			ClientID   *string `json:"client_id"`
		}
		if err := json.Unmarshal(entity, &header); err != nil {
			return nil, err
		}
		for _, ref := range refs {
			if ref.EntityType != header.EntityType {
				continue
			}
			if ref.EntityID != "" && ref.EntityID == header.EntityID {
				result = append(result, append(json.RawMessage(nil), entity...))
				break
			}
			if ref.ClientID != "" && header.ClientID != nil && ref.ClientID == *header.ClientID {
				result = append(result, append(json.RawMessage(nil), entity...))
				break
			}
		}
	}
	return result, nil
}
