package taskmigration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"
)

var (
	// ErrInvalidReplayStore reports construction arguments that cannot provide
	// the transactional guarantees required by ReplayStore.
	ErrInvalidReplayStore = errors.New("invalid replay store")
	// ErrInvalidReplayPage reports an invalid fetch request or a page whose
	// bounds no longer describe its complete fetched event slice.
	ErrInvalidReplayPage = errors.New("invalid replay page")
	// ErrReplayWatermarkConflict is returned when another replay advanced the
	// workspace to a watermark other than this page's exact from/to bounds.
	ErrReplayWatermarkConflict = errors.New("replay watermark conflict")
	// ErrReplayPhaseConflict prevents an old worker from projecting after the
	// workspace has left catching_up/draining or beyond the captured drain
	// cutover revision.
	ErrReplayPhaseConflict = errors.New("replay phase conflict")
)

// ReplayPage is one workspace-scoped, globally ordered outbox suffix. Gaps in
// sequence are valid because sequence is shared by every workspace.
type ReplayPage struct {
	WorkspaceID   string
	MigrationID   string
	FromWatermark int64
	ToWatermark   int64
	Events        []ReplayEvent
}

// ReplayProjector persists the v2 projection for a fetched page. It runs in
// the same transaction as the source-watermark update; returning an error
// rolls both back.
type ReplayProjector func(context.Context, *sql.Tx, []ReplayEvent) error

// ReplayStore owns durable legacy-outbox pagination and atomic watermark
// commits. It is deliberately not wired into production routing; migration
// orchestration must opt into it explicitly.
type ReplayStore struct {
	db      *sql.DB
	dialect Dialect
}

func NewReplayStore(db *sql.DB, dialect Dialect) (*ReplayStore, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalidReplayStore)
	}
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return nil, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidReplayStore, dialect)
	}
	return &ReplayStore{db: db, dialect: dialect}, nil
}

// FetchPage reads the workspace's current persisted watermark, then returns
// only later events for that workspace in strictly increasing global-sequence
// order. An empty result is represented by FromWatermark == ToWatermark.
func (s *ReplayStore) FetchPage(ctx context.Context, workspaceID string, limit int) (ReplayPage, error) {
	if s == nil || s.db == nil || ctx == nil || strings.TrimSpace(workspaceID) == "" || limit <= 0 {
		return ReplayPage{}, fmt.Errorf("%w: workspace and positive limit are required", ErrInvalidReplayPage)
	}

	var fence replayPhaseFence
	stateSQL := `SELECT source_watermark,migration_id,model_version,migration_state,cutover_revision
		FROM workspace_task_domain_state WHERE workspace_id=?`
	if s.dialect == DialectPostgres {
		stateSQL = `SELECT source_watermark,migration_id,model_version,migration_state,cutover_revision
			FROM workspace_task_domain_state WHERE workspace_id=$1`
	}
	if err := s.db.QueryRowContext(ctx, stateSQL, workspaceID).Scan(
		&fence.watermark, &fence.migrationID, &fence.modelVersion, &fence.migrationState, &fence.cutoverRevision,
	); err != nil {
		return ReplayPage{}, fmt.Errorf("load replay watermark for workspace %q: %w", workspaceID, err)
	}
	if fence.watermark < 0 {
		return ReplayPage{}, fmt.Errorf("%w: negative persisted watermark", ErrInvalidReplayPage)
	}
	if err := validateReplayPhaseFence(workspaceID, fence, fence.watermark); err != nil {
		return ReplayPage{}, err
	}
	if strings.TrimSpace(fence.migrationID.String) == "" {
		return ReplayPage{}, fmt.Errorf("%w: workspace %q has no active migration", ErrInvalidReplayPage, workspaceID)
	}

	query := `SELECT sequence,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
FROM task_domain_legacy_outbox
WHERE workspace_id=? AND sequence>?
ORDER BY sequence ASC LIMIT ?`
	if s.dialect == DialectPostgres {
		query = `SELECT sequence,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
FROM task_domain_legacy_outbox
WHERE workspace_id=$1 AND sequence>$2
ORDER BY sequence ASC LIMIT $3`
	}
	rows, err := s.db.QueryContext(ctx, query, workspaceID, fence.watermark, limit)
	if err != nil {
		return ReplayPage{}, fmt.Errorf("fetch legacy replay page for workspace %q: %w", workspaceID, err)
	}
	defer rows.Close()

	events, err := scanReplayEvents(rows)
	if err != nil {
		return ReplayPage{}, err
	}
	page := ReplayPage{
		WorkspaceID: workspaceID, MigrationID: fence.migrationID.String,
		FromWatermark: fence.watermark, ToWatermark: fence.watermark, Events: events,
	}
	if len(events) > 0 {
		page.ToWatermark = events[len(events)-1].Sequence
	}
	if len(page.Events) > 0 {
		if err := validateReplayPage(page); err != nil {
			return ReplayPage{}, err
		}
	}
	return page, nil
}

// CommitPage locks the workspace migration state, verifies this page starts
// at the durable watermark, invokes the projector, and advances the watermark
// in one transaction. A retry after a successful commit observes ToWatermark
// and succeeds without invoking the projector again.
func (s *ReplayStore) CommitPage(ctx context.Context, page ReplayPage, project ReplayProjector) (err error) {
	if s == nil || s.db == nil || ctx == nil || project == nil {
		return fmt.Errorf("%w: store, context, and projector are required", ErrInvalidReplayPage)
	}
	if err := validateReplayPage(page); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin replay commit: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	fence, err := s.lockReplayWatermark(ctx, tx, page.WorkspaceID)
	if err != nil {
		return err
	}
	if err := validateReplayPhaseFence(page.WorkspaceID, fence, page.ToWatermark); err != nil {
		return err
	}
	current := fence.watermark
	if fence.migrationID.String != page.MigrationID {
		return fmt.Errorf("%w: workspace=%s migration=%q page=%q",
			ErrReplayWatermarkConflict, page.WorkspaceID, fence.migrationID.String, page.MigrationID)
	}
	if current != page.FromWatermark && current != page.ToWatermark {
		return fmt.Errorf("%w: workspace=%s current=%d page=%d..%d",
			ErrReplayWatermarkConflict, page.WorkspaceID, current, page.FromWatermark, page.ToWatermark)
	}
	durableEvents, err := s.loadDurableReplayRange(ctx, tx, page)
	if err != nil {
		return err
	}
	if !equalReplayEvents(page.Events, durableEvents) {
		return fmt.Errorf("%w: workspace=%s migration=%s range=%d..%d does not match durable outbox",
			ErrInvalidReplayPage, page.WorkspaceID, page.MigrationID, page.FromWatermark, page.ToWatermark)
	}
	if current == page.ToWatermark {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit idempotent replay page: %w", err)
		}
		return nil
	}

	events := cloneReplayEvents(page.Events)
	if err := project(ctx, tx, events); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, s.advanceWatermarkSQL(), page.ToWatermark, page.WorkspaceID, page.FromWatermark)
	if err != nil {
		return fmt.Errorf("advance replay watermark: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read replay watermark update result: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: workspace=%s current changed from %d",
			ErrReplayWatermarkConflict, page.WorkspaceID, page.FromWatermark)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replay page: %w", err)
	}
	return nil
}

type replayPhaseFence struct {
	watermark       int64
	migrationID     sql.NullString
	modelVersion    string
	migrationState  string
	cutoverRevision sql.NullInt64
}

func validateReplayPhaseFence(workspaceID string, fence replayPhaseFence, pageTo int64) error {
	if fence.modelVersion != string(ModelVersionLegacy) {
		return fmt.Errorf("%w: workspace=%s model_version=%s", ErrReplayPhaseConflict, workspaceID, fence.modelVersion)
	}
	switch MigrationState(fence.migrationState) {
	case MigrationStateCatchingUp:
		if fence.cutoverRevision.Valid {
			return fmt.Errorf("%w: workspace=%s catching_up has cutover revision", ErrReplayPhaseConflict, workspaceID)
		}
	case MigrationStateDraining:
		if !fence.cutoverRevision.Valid || fence.cutoverRevision.Int64 < 0 || pageTo > fence.cutoverRevision.Int64 {
			return fmt.Errorf("%w: workspace=%s page_to=%d cutover_revision=%v", ErrReplayPhaseConflict, workspaceID, pageTo, fence.cutoverRevision)
		}
	default:
		return fmt.Errorf("%w: workspace=%s migration_state=%s", ErrReplayPhaseConflict, workspaceID, fence.migrationState)
	}
	return nil
}

func (s *ReplayStore) lockReplayWatermark(ctx context.Context, tx *sql.Tx, workspaceID string) (replayPhaseFence, error) {
	if s.dialect == DialectSQLite {
		// database/sql cannot issue BEGIN IMMEDIATE and still expose *sql.Tx.
		// Making a harmless write the first statement acquires SQLite's reserved
		// writer lock before reading the state, which provides the same
		// single-writer semantics for the remainder of this transaction.
		result, err := tx.ExecContext(ctx, `UPDATE workspace_task_domain_state
SET source_watermark=source_watermark
WHERE workspace_id=?`, workspaceID)
		if err != nil {
			return replayPhaseFence{}, fmt.Errorf("lock SQLite replay watermark: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return replayPhaseFence{}, fmt.Errorf("read SQLite replay lock result: %w", err)
		}
		if rows != 1 {
			return replayPhaseFence{}, fmt.Errorf("load replay watermark for workspace %q: %w", workspaceID, sql.ErrNoRows)
		}
		var fence replayPhaseFence
		if err := tx.QueryRowContext(ctx, `SELECT source_watermark,migration_id,model_version,migration_state,cutover_revision
			FROM workspace_task_domain_state WHERE workspace_id=?`, workspaceID).Scan(
			&fence.watermark, &fence.migrationID, &fence.modelVersion, &fence.migrationState, &fence.cutoverRevision,
		); err != nil {
			return replayPhaseFence{}, fmt.Errorf("load locked replay watermark: %w", err)
		}
		return fence, nil
	}

	var fence replayPhaseFence
	if err := tx.QueryRowContext(ctx, `SELECT source_watermark,migration_id,model_version,migration_state,cutover_revision
		FROM workspace_task_domain_state
		WHERE workspace_id=$1 FOR UPDATE`, workspaceID).Scan(
		&fence.watermark, &fence.migrationID, &fence.modelVersion, &fence.migrationState, &fence.cutoverRevision,
	); err != nil {
		return replayPhaseFence{}, fmt.Errorf("load locked replay watermark: %w", err)
	}
	return fence, nil
}

func (s *ReplayStore) loadDurableReplayRange(ctx context.Context, tx *sql.Tx, page ReplayPage) ([]ReplayEvent, error) {
	query := `SELECT sequence,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
FROM task_domain_legacy_outbox
WHERE workspace_id=? AND sequence>? AND sequence<=?
ORDER BY sequence ASC`
	if s.dialect == DialectPostgres {
		query = `SELECT sequence,entity_kind,entity_id,operation,source_logical_version,row_image,tombstone_image
FROM task_domain_legacy_outbox
WHERE workspace_id=$1 AND sequence>$2 AND sequence<=$3
ORDER BY sequence ASC`
	}
	rows, err := tx.QueryContext(ctx, query, page.WorkspaceID, page.FromWatermark, page.ToWatermark)
	if err != nil {
		return nil, fmt.Errorf("reload durable replay range for workspace %q: %w", page.WorkspaceID, err)
	}
	defer rows.Close()
	return scanReplayEvents(rows)
}

func (s *ReplayStore) advanceWatermarkSQL() string {
	if s.dialect == DialectPostgres {
		return `UPDATE workspace_task_domain_state
SET source_watermark=$1,revision=revision+1,updated_at=CURRENT_TIMESTAMP
WHERE workspace_id=$2 AND source_watermark=$3`
	}
	return `UPDATE workspace_task_domain_state
SET source_watermark=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
WHERE workspace_id=? AND source_watermark=?`
}

func validateReplayPage(page ReplayPage) error {
	if strings.TrimSpace(page.WorkspaceID) == "" || strings.TrimSpace(page.MigrationID) == "" || page.FromWatermark < 0 || page.ToWatermark <= page.FromWatermark || len(page.Events) == 0 {
		return fmt.Errorf("%w: non-empty advancing page is required", ErrInvalidReplayPage)
	}
	if err := validateReplayEvents(page.FromWatermark, page.Events); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidReplayPage, err)
	}
	for _, event := range page.Events {
		if event.Sequence <= page.FromWatermark {
			return fmt.Errorf("%w: sequence %d is not after watermark %d", ErrInvalidReplayPage, event.Sequence, page.FromWatermark)
		}
		switch event.Operation {
		case ReplayUpsert:
			if event.TombstoneImage != nil {
				return fmt.Errorf("%w: upsert sequence %d contains tombstone", ErrInvalidReplayPage, event.Sequence)
			}
		case ReplayDelete:
			if event.AfterImage != nil {
				return fmt.Errorf("%w: delete sequence %d contains after-image", ErrInvalidReplayPage, event.Sequence)
			}
		}
	}
	if last := page.Events[len(page.Events)-1].Sequence; page.ToWatermark != last {
		return fmt.Errorf("%w: to watermark %d does not equal fetched last sequence %d", ErrInvalidReplayPage, page.ToWatermark, last)
	}
	return nil
}

func scanReplayEvents(rows *sql.Rows) ([]ReplayEvent, error) {
	var events []ReplayEvent
	for rows.Next() {
		var event ReplayEvent
		var kind, operation string
		var rowImage, tombstoneImage sql.NullString
		if err := rows.Scan(
			&event.Sequence, &kind, &event.SourceID, &operation, &event.LogicalVersion,
			&rowImage, &tombstoneImage,
		); err != nil {
			return nil, fmt.Errorf("scan legacy replay event: %w", err)
		}
		event.EntityKind = ReplayEntityKind(kind)
		event.Operation = ReplayOperation(operation)
		var err error
		if rowImage.Valid {
			event.AfterImage, err = decodeReplayImage(rowImage.String)
			if err != nil {
				return nil, fmt.Errorf("decode row image at sequence %d: %w", event.Sequence, err)
			}
		}
		if tombstoneImage.Valid {
			event.TombstoneImage, err = decodeReplayImage(tombstoneImage.String)
			if err != nil {
				return nil, fmt.Errorf("decode tombstone image at sequence %d: %w", event.Sequence, err)
			}
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy replay page: %w", err)
	}
	return events, nil
}

func equalReplayEvents(left, right []ReplayEvent) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Sequence != right[index].Sequence ||
			left[index].EntityKind != right[index].EntityKind ||
			left[index].SourceID != right[index].SourceID ||
			left[index].Operation != right[index].Operation ||
			left[index].LogicalVersion != right[index].LogicalVersion ||
			!equalReplayImage(left[index].AfterImage, right[index].AfterImage) ||
			!equalReplayImage(left[index].TombstoneImage, right[index].TombstoneImage) {
			return false
		}
	}
	return true
}

func equalReplayImage(left, right ReplayImage) bool {
	if (left == nil) != (right == nil) {
		return false
	}
	return maps.Equal(left, right)
}

func decodeReplayImage(raw string) (ReplayImage, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("replay image must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("replay image contains trailing JSON")
		}
		return nil, fmt.Errorf("decode trailing replay image data: %w", err)
	}
	image := make(ReplayImage, len(object))
	for key, value := range object {
		normalized, err := normalizeReplayImageValue(value)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		image[key] = normalized
	}
	return image, nil
}

func normalizeReplayImageValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case nil:
		return "null", nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func cloneReplayEvents(events []ReplayEvent) []ReplayEvent {
	result := make([]ReplayEvent, len(events))
	for index, event := range events {
		event.AfterImage = cloneReplayImage(event.AfterImage)
		event.TombstoneImage = cloneReplayImage(event.TombstoneImage)
		result[index] = event
	}
	return result
}
