package mobilev2sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
)

type SQLDialect string

const (
	SQLDialectSQLite   SQLDialect = "sqlite"
	SQLDialectPostgres SQLDialect = "postgres"
)

var (
	ErrInvalidSQLRepository = errors.New("invalid mobile-v2 sync repository")
	ErrSnapshotExpired      = errors.New("mobile-v2 snapshot expired")
	ErrSnapshotMismatch     = errors.New("mobile-v2 snapshot binding mismatch")
	ErrCursorMismatch       = errors.New("mobile-v2 cursor binding mismatch")
)

type CreateSnapshotInput struct {
	Binding         TokenBinding
	ProjectionAsOf  time.Time
	ScopeValidUntil *time.Time
	ExpiresAt       time.Time
	PageSize        int
}

type SnapshotProjector func(context.Context, *sql.Tx, uint64) ([]json.RawMessage, error)

type StoredSnapshotPage struct {
	SnapshotID               string
	AsOfSequence             string
	SnapshotCursor           string
	ProjectionAsOf           time.Time
	ProjectionTimeZone       *string
	ScopeGeneration          string
	ScopeValidUntil          *time.Time
	EntitiesJSON             json.RawMessage
	PageIndex                int
	PageChecksum             string
	SnapshotManifestChecksum string
	PageCount                int
	ExpiresAt                time.Time
}

type ScopeChangeInput struct {
	WorkspaceID          string
	Scope                ScopeName
	CausedByCommandID    *string
	OriginDeviceClientID *string
	ReceiptJSON          json.RawMessage
	EntitiesJSON         json.RawMessage
	CommittedAt          time.Time
}

type StoredChangeBatch struct {
	Sequence             string
	CausedByCommandID    *string
	OriginDeviceClientID *string
	ReceiptJSON          json.RawMessage
	EntitiesJSON         json.RawMessage
}

type StoredChangePage struct {
	Changes    []StoredChangeBatch
	NextCursor string
	HasMore    bool
}

type SQLRepository struct {
	db      *sql.DB
	dialect SQLDialect
	tokens  TokenCodec
}

func NewSQLRepository(db *sql.DB, dialect SQLDialect, tokens TokenCodec) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect, tokens: tokens}
}

func (repository *SQLRepository) CreateSnapshot(
	ctx context.Context,
	input CreateSnapshotInput,
	project SnapshotProjector,
) (StoredSnapshotPage, error) {
	if err := repository.validate(); err != nil {
		return StoredSnapshotPage{}, err
	}
	if err := validateCreateSnapshot(input, project); err != nil {
		return StoredSnapshotPage{}, err
	}

	tx, err := repository.db.BeginTx(ctx, repository.snapshotTxOptions())
	if err != nil {
		return StoredSnapshotPage{}, err
	}
	defer tx.Rollback()

	sequence, err := repository.lockCommitHead(ctx, tx, input.Binding.WorkspaceID)
	if err != nil {
		return StoredSnapshotPage{}, err
	}
	entities, err := project(ctx, tx, sequence)
	if err != nil {
		return StoredSnapshotPage{}, err
	}
	pagePayloads, err := materializePages(entities, input.PageSize)
	if err != nil {
		return StoredSnapshotPage{}, err
	}

	snapshotID := uuid.NewString()
	asOfSequence := strconv.FormatUint(sequence, 10)
	snapshotCursor, err := repository.tokens.EncodeChangeCursor(ChangeCursorToken{
		Binding: input.Binding, Sequence: asOfSequence,
	})
	if err != nil {
		return StoredSnapshotPage{}, err
	}
	pageChecksums := make([]string, len(pagePayloads))
	for index, payload := range pagePayloads {
		pageChecksums[index], err = mobilev2contract.PageChecksum(snapshotID, index, asOfSequence, payload)
		if err != nil {
			return StoredSnapshotPage{}, err
		}
	}
	manifestChecksum, err := mobilev2contract.ManifestChecksum(
		snapshotID, asOfSequence, input.Binding.ScopeGeneration, pageChecksums,
	)
	if err != nil {
		return StoredSnapshotPage{}, err
	}

	projectionAsOf := input.ProjectionAsOf.UTC().Truncate(time.Millisecond)
	expiresAt := input.ExpiresAt.UTC().Truncate(time.Millisecond)
	validUntil := utcTimePointer(input.ScopeValidUntil)
	if err := repository.insertSnapshotSession(
		ctx, tx, input, snapshotID, sequence, snapshotCursor, manifestChecksum,
		projectionAsOf, validUntil, expiresAt, len(pagePayloads),
	); err != nil {
		return StoredSnapshotPage{}, err
	}
	for index, payload := range pagePayloads {
		if _, err := tx.ExecContext(ctx, repository.bind(`INSERT INTO mobile_v2_snapshot_pages
			(snapshot_id,page_index,page_checksum,entities_json) VALUES (?,?,?,?)`),
			snapshotID, index, pageChecksums[index], string(payload)); err != nil {
			return StoredSnapshotPage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return StoredSnapshotPage{}, err
	}
	return StoredSnapshotPage{
		SnapshotID: snapshotID, AsOfSequence: asOfSequence, SnapshotCursor: snapshotCursor,
		ProjectionAsOf: projectionAsOf, ProjectionTimeZone: cloneString(input.Binding.ProjectionTimeZone),
		ScopeGeneration: input.Binding.ScopeGeneration, ScopeValidUntil: validUntil,
		EntitiesJSON: pagePayloads[0], PageIndex: 0, PageChecksum: pageChecksums[0],
		SnapshotManifestChecksum: manifestChecksum, PageCount: len(pagePayloads), ExpiresAt: expiresAt,
	}, nil
}

func (repository *SQLRepository) ReadSnapshotPage(
	ctx context.Context,
	binding TokenBinding,
	snapshotID string,
	pageIndex int,
	now time.Time,
) (StoredSnapshotPage, error) {
	if err := repository.validate(); err != nil {
		return StoredSnapshotPage{}, err
	}
	if !validBinding(binding) || strings.TrimSpace(snapshotID) == "" || pageIndex < 0 {
		return StoredSnapshotPage{}, ErrSnapshotMismatch
	}
	var (
		page               StoredSnapshotPage
		workspaceID        string
		scope              ScopeName
		contractEpoch      uint64
		runtimeEpoch       int64
		taskModelVersion   int
		projectionTimeZone sql.NullString
		projectionAsOf     scannedTime
		scopeValidUntil    scannedTime
		expiresAt          scannedTime
		asOfSequence       uint64
		entitiesJSON       []byte
	)
	err := repository.db.QueryRowContext(ctx, repository.bind(`SELECT
		s.workspace_id,s.scope,s.as_of_sequence,s.contract_epoch,s.runtime_epoch,s.task_model_version,
		s.projection_as_of,s.projection_time_zone,s.scope_generation,s.scope_valid_until,
		s.snapshot_cursor,s.manifest_checksum,s.page_count,s.expires_at,
		p.page_index,p.page_checksum,p.entities_json
		FROM mobile_v2_snapshot_sessions s
		JOIN mobile_v2_snapshot_pages p ON p.snapshot_id=s.snapshot_id
		WHERE s.snapshot_id=? AND p.page_index=?`), snapshotID, pageIndex).Scan(
		&workspaceID, &scope, &asOfSequence, &contractEpoch, &runtimeEpoch, &taskModelVersion,
		&projectionAsOf, &projectionTimeZone, &page.ScopeGeneration, &scopeValidUntil,
		&page.SnapshotCursor, &page.SnapshotManifestChecksum, &page.PageCount, &expiresAt,
		&page.PageIndex, &page.PageChecksum, &entitiesJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredSnapshotPage{}, ErrSnapshotExpired
	}
	if err != nil {
		return StoredSnapshotPage{}, err
	}
	page.SnapshotID = snapshotID
	page.AsOfSequence = strconv.FormatUint(asOfSequence, 10)
	page.EntitiesJSON = append(json.RawMessage(nil), entitiesJSON...)
	page.ProjectionAsOf = projectionAsOf.Time
	page.ProjectionTimeZone = nullStringPointer(projectionTimeZone)
	page.ScopeValidUntil = scannedTimePointer(scopeValidUntil)
	page.ExpiresAt = expiresAt.Time
	if !now.UTC().Before(page.ExpiresAt.UTC()) {
		return StoredSnapshotPage{}, ErrSnapshotExpired
	}
	if workspaceID != binding.WorkspaceID || scope != binding.Scope ||
		strconv.FormatUint(contractEpoch, 10) != binding.ContractEpoch ||
		strconv.FormatInt(runtimeEpoch, 10) != binding.RuntimeEpoch ||
		taskModelVersion != binding.TaskModelVersion ||
		page.ScopeGeneration != binding.ScopeGeneration ||
		!equalOptionalString(page.ProjectionTimeZone, binding.ProjectionTimeZone) {
		return StoredSnapshotPage{}, ErrSnapshotMismatch
	}
	return page, nil
}

func (repository *SQLRepository) DeleteExpiredSnapshots(ctx context.Context, now time.Time) (int64, error) {
	if err := repository.validate(); err != nil {
		return 0, err
	}
	result, err := repository.db.ExecContext(ctx,
		repository.bind(`DELETE FROM mobile_v2_snapshot_sessions WHERE expires_at<=?`), now.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (repository *SQLRepository) AppendScopeChange(
	ctx context.Context,
	tx *sql.Tx,
	input ScopeChangeInput,
) (uint64, error) {
	if err := repository.validate(); err != nil {
		return 0, err
	}
	if tx == nil || !validScopeChange(input) {
		return 0, ErrCursorMismatch
	}
	if err := repository.lockCommitHeadExclusive(ctx, tx, input.WorkspaceID); err != nil {
		return 0, err
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, repository.bind(`UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? RETURNING latest_sequence`), input.WorkspaceID).Scan(&sequence); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, repository.bind(`INSERT INTO mobile_v2_scope_change_batches
		(workspace_id,scope,sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json,committed_at)
		VALUES (?,?,?,?,?,?,?,?)`),
		input.WorkspaceID, input.Scope, sequence, nullableString(input.CausedByCommandID),
		nullableString(input.OriginDeviceClientID), nullableJSON(input.ReceiptJSON),
		string(input.EntitiesJSON), input.CommittedAt.UTC().Truncate(time.Millisecond),
	)
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

func (repository *SQLRepository) ReadChanges(
	ctx context.Context,
	binding TokenBinding,
	cursor string,
	limit int,
) (StoredChangePage, error) {
	if err := repository.validate(); err != nil {
		return StoredChangePage{}, err
	}
	if !validBinding(binding) || limit < 1 || limit > 1000 {
		return StoredChangePage{}, ErrCursorMismatch
	}
	var after uint64
	if cursor != "" {
		decoded, err := repository.tokens.DecodeChangeCursor(cursor, binding)
		if err != nil {
			return StoredChangePage{}, ErrCursorMismatch
		}
		after, err = strconv.ParseUint(decoded.Sequence, 10, 64)
		if err != nil {
			return StoredChangePage{}, ErrCursorMismatch
		}
	}
	var latest uint64
	err := repository.db.QueryRowContext(ctx, repository.bind(
		`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=?`,
	), binding.WorkspaceID).Scan(&latest)
	if errors.Is(err, sql.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return StoredChangePage{}, err
	}
	if after > latest {
		return StoredChangePage{}, ErrCursorMismatch
	}
	rows, err := repository.db.QueryContext(ctx, repository.bind(`SELECT
		sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json
		FROM mobile_v2_scope_change_batches
		WHERE workspace_id=? AND scope=? AND sequence>?
		ORDER BY sequence LIMIT ?`), binding.WorkspaceID, binding.Scope, after, limit+1)
	if err != nil {
		return StoredChangePage{}, err
	}
	defer rows.Close()
	changes := make([]StoredChangeBatch, 0, limit+1)
	for rows.Next() {
		var (
			sequence     uint64
			commandID    sql.NullString
			deviceID     sql.NullString
			receiptJSON  []byte
			entitiesJSON []byte
		)
		if err := rows.Scan(&sequence, &commandID, &deviceID, &receiptJSON, &entitiesJSON); err != nil {
			return StoredChangePage{}, err
		}
		changes = append(changes, StoredChangeBatch{
			Sequence:             strconv.FormatUint(sequence, 10),
			CausedByCommandID:    nullStringPointer(commandID),
			OriginDeviceClientID: nullStringPointer(deviceID),
			ReceiptJSON:          append(json.RawMessage(nil), receiptJSON...),
			EntitiesJSON:         append(json.RawMessage(nil), entitiesJSON...),
		})
	}
	if err := rows.Err(); err != nil {
		return StoredChangePage{}, err
	}
	hasMore := len(changes) > limit
	if hasMore {
		changes = changes[:limit]
	}
	nextSequence := latest
	if hasMore && len(changes) > 0 {
		nextSequence, _ = strconv.ParseUint(changes[len(changes)-1].Sequence, 10, 64)
	}
	nextCursor, err := repository.tokens.EncodeChangeCursor(ChangeCursorToken{
		Binding: binding, Sequence: strconv.FormatUint(nextSequence, 10),
	})
	if err != nil {
		return StoredChangePage{}, err
	}
	return StoredChangePage{Changes: changes, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func (repository *SQLRepository) insertSnapshotSession(
	ctx context.Context,
	tx *sql.Tx,
	input CreateSnapshotInput,
	snapshotID string,
	sequence uint64,
	snapshotCursor string,
	manifestChecksum string,
	projectionAsOf time.Time,
	validUntil *time.Time,
	expiresAt time.Time,
	pageCount int,
) error {
	_, err := tx.ExecContext(ctx, repository.bind(`INSERT INTO mobile_v2_snapshot_sessions
		(snapshot_id,workspace_id,scope,as_of_sequence,contract_epoch,runtime_epoch,task_model_version,
		projection_as_of,projection_time_zone,scope_generation,scope_valid_until,
		snapshot_cursor,manifest_checksum,page_count,expires_at,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		snapshotID, input.Binding.WorkspaceID, input.Binding.Scope, sequence,
		input.Binding.ContractEpoch, input.Binding.RuntimeEpoch, input.Binding.TaskModelVersion,
		projectionAsOf, nullableString(input.Binding.ProjectionTimeZone), input.Binding.ScopeGeneration,
		nullableTime(validUntil), snapshotCursor, manifestChecksum, pageCount, expiresAt, projectionAsOf,
	)
	return err
}

func (repository *SQLRepository) lockCommitHead(ctx context.Context, tx *sql.Tx, workspaceID string) (uint64, error) {
	if repository.dialect == SQLDialectPostgres {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mobile_v2_commit_heads(workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			workspaceID); err != nil {
			return 0, err
		}
		var sequence uint64
		err := tx.QueryRowContext(ctx,
			`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=$1 FOR SHARE`,
			workspaceID).Scan(&sequence)
		return sequence, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO mobile_v2_commit_heads(workspace_id) VALUES (?)`, workspaceID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE mobile_v2_commit_heads SET latest_sequence=latest_sequence WHERE workspace_id=?`, workspaceID); err != nil {
		return 0, err
	}
	var sequence uint64
	err := tx.QueryRowContext(ctx,
		`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=?`, workspaceID).Scan(&sequence)
	return sequence, err
}

func (repository *SQLRepository) lockCommitHeadExclusive(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	if repository.dialect == SQLDialectPostgres {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mobile_v2_commit_heads(workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`,
			workspaceID); err != nil {
			return err
		}
		var sequence uint64
		return tx.QueryRowContext(ctx,
			`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=$1 FOR UPDATE`,
			workspaceID).Scan(&sequence)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO mobile_v2_commit_heads(workspace_id) VALUES (?)`, workspaceID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE mobile_v2_commit_heads SET latest_sequence=latest_sequence WHERE workspace_id=?`, workspaceID)
	return err
}

func (repository *SQLRepository) validate() error {
	if repository == nil || repository.db == nil ||
		(repository.dialect != SQLDialectSQLite && repository.dialect != SQLDialectPostgres) {
		return ErrInvalidSQLRepository
	}
	return nil
}

func (repository *SQLRepository) snapshotTxOptions() *sql.TxOptions {
	if repository.dialect == SQLDialectPostgres {
		return &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: false}
	}
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func (repository *SQLRepository) bind(query string) string {
	if repository.dialect != SQLDialectPostgres {
		return query
	}
	var builder strings.Builder
	parameter := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&builder, "$%d", parameter)
			parameter++
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func validateCreateSnapshot(input CreateSnapshotInput, project SnapshotProjector) error {
	if !validBinding(input.Binding) || project == nil || input.PageSize < 1 || input.PageSize > 1000 ||
		input.ProjectionAsOf.IsZero() || input.ExpiresAt.IsZero() ||
		!input.ProjectionAsOf.Before(input.ExpiresAt) {
		return ErrSnapshotMismatch
	}
	sliding := input.Binding.Scope == ScopeIPhoneOccurrenceWindow ||
		input.Binding.Scope == ScopeWatchOccurrenceWindow
	if sliding {
		if input.ScopeValidUntil == nil || !input.ProjectionAsOf.Before(*input.ScopeValidUntil) {
			return ErrSnapshotMismatch
		}
	} else if input.ScopeValidUntil != nil {
		return ErrSnapshotMismatch
	}
	return nil
}

func materializePages(entities []json.RawMessage, pageSize int) ([]json.RawMessage, error) {
	for _, entity := range entities {
		if !json.Valid(entity) || len(bytes.TrimSpace(entity)) == 0 || bytes.TrimSpace(entity)[0] != '{' {
			return nil, fmt.Errorf("invalid mobile-v2 snapshot entity")
		}
	}
	pageCount := (len(entities) + pageSize - 1) / pageSize
	if pageCount == 0 {
		pageCount = 1
	}
	pages := make([]json.RawMessage, 0, pageCount)
	for pageIndex := 0; pageIndex < pageCount; pageIndex++ {
		start := pageIndex * pageSize
		end := start + pageSize
		if end > len(entities) {
			end = len(entities)
		}
		payload, err := json.Marshal(entities[start:end])
		if err != nil {
			return nil, err
		}
		pages = append(pages, payload)
	}
	return pages, nil
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC().Truncate(time.Millisecond)
	return &result
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func scannedTimePointer(value scannedTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func validScopeChange(input ScopeChangeInput) bool {
	if strings.TrimSpace(input.WorkspaceID) == "" || !validScope(input.Scope) ||
		input.CommittedAt.IsZero() || !validJSONArray(input.EntitiesJSON) {
		return false
	}
	commandBacked := input.CausedByCommandID != nil || input.OriginDeviceClientID != nil || len(input.ReceiptJSON) > 0
	if !commandBacked {
		return true
	}
	return input.CausedByCommandID != nil && strings.TrimSpace(*input.CausedByCommandID) != "" &&
		input.OriginDeviceClientID != nil && strings.TrimSpace(*input.OriginDeviceClientID) != "" &&
		validJSONObject(input.ReceiptJSON)
}

func validScope(scope ScopeName) bool {
	switch scope {
	case ScopeIPhoneContent, ScopeIPhoneTaskCore, ScopeIPhoneOccurrenceWindow, ScopeWatchOccurrenceWindow:
		return true
	default:
		return false
	}
}

func validJSONArray(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && trimmed[0] == '[' && json.Valid(trimmed)
}

func validJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

type scannedTime struct {
	Time  time.Time
	Valid bool
}

func (value *scannedTime) Scan(source any) error {
	if source == nil {
		value.Time = time.Time{}
		value.Valid = false
		return nil
	}
	if timestamp, ok := source.(time.Time); ok {
		value.Time = timestamp.UTC()
		value.Valid = true
		return nil
	}
	var raw string
	switch timestamp := source.(type) {
	case string:
		raw = timestamp
	case []byte:
		raw = string(timestamp)
	default:
		return fmt.Errorf("unsupported mobile-v2 timestamp type %T", source)
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	} {
		timestamp, err := time.Parse(layout, raw)
		if err == nil {
			value.Time = timestamp.UTC()
			value.Valid = true
			return nil
		}
	}
	return fmt.Errorf("invalid mobile-v2 timestamp %q", raw)
}
