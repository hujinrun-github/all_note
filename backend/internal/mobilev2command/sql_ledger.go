package mobilev2command

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
)

type SQLDialect string

const (
	SQLDialectSQLite   SQLDialect = "sqlite"
	SQLDialectPostgres SQLDialect = "postgres"
)

var ErrInvalidSQLDialect = errors.New("mobile-v2 invalid SQL dialect")

type TerminalCommit struct {
	Command      Command
	Result       DomainResult
	ScopeChanges []ScopeChange
	CompletedAt  time.Time
}

type ScopeChange struct {
	Scope       mobilev2sync.ScopeName
	AfterImages [][]byte
}

type DynamicCommitResult struct {
	DomainResult
	ScopeChanges []ScopeChange
}

type SQLRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type SQLLedger struct {
	db      *sql.DB
	dialect SQLDialect
}

func NewSQLLedger(db *sql.DB, dialect SQLDialect) *SQLLedger {
	return &SQLLedger{db: db, dialect: dialect}
}

func (ledger *SQLLedger) Commit(
	ctx context.Context,
	input TerminalCommit,
	apply func(context.Context, *sql.Tx) error,
) (Response, error) {
	return ledger.CommitDynamic(ctx, input.Command, input.CompletedAt, func(ctx context.Context, tx *sql.Tx) (DynamicCommitResult, error) {
		if apply != nil {
			if err := apply(ctx, tx); err != nil {
				return DynamicCommitResult{}, err
			}
		}
		return DynamicCommitResult{DomainResult: input.Result, ScopeChanges: input.ScopeChanges}, nil
	})
}

func (ledger *SQLLedger) CommitDynamic(
	ctx context.Context,
	command Command,
	completedAt time.Time,
	apply func(context.Context, *sql.Tx) (DynamicCommitResult, error),
) (Response, error) {
	if err := ledger.validate(); err != nil {
		return Response{}, err
	}
	if err := validateLedgerCommand(command); err != nil {
		return Response{}, err
	}
	if apply == nil {
		return Response{}, ErrInvalidTerminalStatus
	}
	completedAt = completedAt.UTC()
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	tx, err := ledger.db.BeginTx(ctx, ledger.txOptions())
	if err != nil {
		return Response{}, err
	}
	defer tx.Rollback()
	prepared, proceed, err := ledger.PrepareOnRunner(ctx, tx, command)
	if err != nil {
		return Response{}, err
	}
	if !proceed {
		if err := tx.Commit(); err != nil {
			return Response{}, err
		}
		return prepared, nil
	}
	dynamic, err := apply(ctx, tx)
	if err != nil {
		return Response{}, err
	}
	if dynamic.Status == StatusRetryLater {
		return Response{RetryLater: true}, nil
	}
	response, err := ledger.FinalizeOnRunner(ctx, tx, command, completedAt, dynamic)
	if err != nil {
		return Response{}, err
	}
	if err := tx.Commit(); err != nil {
		return Response{}, err
	}
	return response, nil
}

// PrepareOnRunner locks the per-workspace ledger head and performs the
// receipt-first idempotency lookup inside an existing tenant transaction.
func (ledger *SQLLedger) PrepareOnRunner(
	ctx context.Context,
	runner SQLRunner,
	command Command,
) (Response, bool, error) {
	if err := ledger.validateDialect(); err != nil {
		return Response{}, false, err
	}
	if runner == nil {
		return Response{}, false, ErrInvalidSQLDialect
	}
	if err := validateLedgerCommand(command); err != nil {
		return Response{}, false, err
	}
	if err := ledger.ensureAndLockHead(ctx, runner, command.WorkspaceID); err != nil {
		return Response{}, false, err
	}
	stored, found, err := ledger.lookup(ctx, runner, command.WorkspaceID, command.OriginDeviceClientID, command.CommandID)
	if err != nil {
		return Response{}, false, err
	}
	if found {
		if stored.RequestDigest != command.RequestDigest {
			return Response{}, false, ErrRequestDigestMismatch
		}
		copy := cloneReceipt(stored)
		return Response{Receipt: &copy, Replayed: true}, false, nil
	}
	complete, err := ledger.historyComplete(ctx, runner, command.WorkspaceID)
	if err != nil {
		return Response{}, false, err
	}
	if !complete {
		return Response{}, false, ErrReceiptHistoryAmbiguous
	}
	return Response{}, true, nil
}

// FinalizeOnRunner writes the sequence, terminal receipt, global audit batch,
// and every scope batch into the same existing transaction as the domain
// effect.
func (ledger *SQLLedger) FinalizeOnRunner(
	ctx context.Context,
	runner SQLRunner,
	command Command,
	completedAt time.Time,
	dynamic DynamicCommitResult,
) (Response, error) {
	if err := ledger.validateDialect(); err != nil {
		return Response{}, err
	}
	if runner == nil {
		return Response{}, ErrInvalidSQLDialect
	}
	completedAt = completedAt.UTC()
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	if !terminalStatus(dynamic.Status) {
		return Response{}, ErrInvalidTerminalStatus
	}
	if err := validateScopeChanges(dynamic.ScopeChanges); err != nil {
		return Response{}, err
	}

	sequence, err := ledger.nextSequence(ctx, runner, command.WorkspaceID)
	if err != nil {
		return Response{}, err
	}
	receipt := Receipt{
		WorkspaceID: command.WorkspaceID, OriginDeviceClientID: command.OriginDeviceClientID,
		CommandID: command.CommandID, RequestDigest: command.RequestDigest, Status: dynamic.Status,
		CommitSequence: sequence, IdentityMappings: cloneMappings(dynamic.IdentityMappings),
		AffectedRevisions: cloneRevisions(dynamic.AffectedRevisions), CompletedAt: completedAt,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return Response{}, err
	}
	afterImagesJSON, err := marshalAfterImages(dynamic.AfterImages)
	if err != nil {
		return Response{}, err
	}
	if _, err := runner.ExecContext(ctx, ledger.bind(`INSERT INTO mobile_v2_command_receipts
		(workspace_id,origin_device_client_id,command_id,request_digest,command_type,status,commit_sequence,receipt_json,completed_at)
		VALUES (?,?,?,?,?,?,?,?,?)`),
		command.WorkspaceID, command.OriginDeviceClientID, command.CommandID, command.RequestDigest,
		command.CommandType, dynamic.Status, sequence, string(receiptJSON), completedAt); err != nil {
		return Response{}, err
	}
	if _, err := runner.ExecContext(ctx, ledger.bind(`INSERT INTO mobile_v2_change_batches
		(workspace_id,sequence,caused_by_command_id,origin_device_client_id,receipt_json,after_images_json,committed_at)
		VALUES (?,?,?,?,?,?,?)`),
		command.WorkspaceID, sequence, command.CommandID, command.OriginDeviceClientID,
		string(receiptJSON), string(afterImagesJSON), completedAt); err != nil {
		return Response{}, err
	}
	for _, change := range dynamic.ScopeChanges {
		entitiesJSON, err := marshalAfterImages(change.AfterImages)
		if err != nil {
			return Response{}, err
		}
		if _, err := runner.ExecContext(ctx, ledger.bind(`INSERT INTO mobile_v2_scope_change_batches
			(workspace_id,scope,sequence,caused_by_command_id,origin_device_client_id,receipt_json,entities_json,committed_at)
			VALUES (?,?,?,?,?,?,?,?)`),
			command.WorkspaceID, change.Scope, sequence, command.CommandID, command.OriginDeviceClientID,
			string(receiptJSON), string(entitiesJSON), completedAt); err != nil {
			return Response{}, err
		}
	}
	copy := cloneReceipt(receipt)
	return Response{Receipt: &copy}, nil
}

func (ledger *SQLLedger) Lookup(
	ctx context.Context,
	workspaceID string,
	originDeviceClientID string,
	commandID string,
) (Receipt, bool, error) {
	if err := ledger.validate(); err != nil {
		return Receipt{}, false, err
	}
	return ledger.lookup(ctx, ledger.db, workspaceID, originDeviceClientID, commandID)
}

func (ledger *SQLLedger) LookupOnRunner(
	ctx context.Context,
	runner SQLRunner,
	workspaceID string,
	originDeviceClientID string,
	commandID string,
) (Receipt, bool, error) {
	if err := ledger.validateDialect(); err != nil {
		return Receipt{}, false, err
	}
	if runner == nil {
		return Receipt{}, false, ErrInvalidSQLDialect
	}
	return ledger.lookup(ctx, runner, workspaceID, originDeviceClientID, commandID)
}

// CurrentSequenceOnRunner reads the locked workspace commit head. Callers use
// current+1 when producing transaction-local after-images whose payload
// contains the sequence that FinalizeOnRunner will reserve.
func (ledger *SQLLedger) CurrentSequenceOnRunner(
	ctx context.Context,
	runner SQLRunner,
	workspaceID string,
) (uint64, error) {
	if err := ledger.validateDialect(); err != nil {
		return 0, err
	}
	if runner == nil {
		return 0, ErrInvalidSQLDialect
	}
	var sequence uint64
	err := runner.QueryRowContext(ctx, ledger.bind(
		`SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=?`,
	), workspaceID).Scan(&sequence)
	return sequence, err
}

func (ledger *SQLLedger) ChangesAfter(ctx context.Context, workspaceID string, sequence uint64) ([]ChangeBatch, error) {
	if err := ledger.validate(); err != nil {
		return nil, err
	}
	rows, err := ledger.db.QueryContext(ctx, ledger.bind(`SELECT sequence,caused_by_command_id,origin_device_client_id,receipt_json,after_images_json
		FROM mobile_v2_change_batches WHERE workspace_id=? AND sequence>? ORDER BY sequence`), workspaceID, sequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ChangeBatch, 0)
	for rows.Next() {
		var change ChangeBatch
		var receiptJSON, afterImagesJSON []byte
		if err := rows.Scan(&change.Sequence, &change.CausedByCommandID, &change.OriginDeviceClientID, &receiptJSON, &afterImagesJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(receiptJSON, &change.Receipt); err != nil {
			return nil, fmt.Errorf("decode mobile-v2 receipt change: %w", err)
		}
		change.AfterImages, err = unmarshalAfterImages(afterImagesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode mobile-v2 after-images: %w", err)
		}
		result = append(result, change)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (ledger *SQLLedger) CompactChanges(ctx context.Context, workspaceID string, through uint64) error {
	if err := ledger.validate(); err != nil {
		return err
	}
	_, err := ledger.db.ExecContext(ctx, ledger.bind(`DELETE FROM mobile_v2_change_batches WHERE workspace_id=? AND sequence<=?`), workspaceID, through)
	return err
}

func (ledger *SQLLedger) MarkReceiptHistoryAmbiguous(ctx context.Context, workspaceID string) error {
	if err := ledger.validate(); err != nil {
		return err
	}
	tx, err := ledger.db.BeginTx(ctx, ledger.txOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ledger.ensureAndLockHead(ctx, tx, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, ledger.bind(`UPDATE mobile_v2_commit_heads
		SET receipt_history_complete=?,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=?`), false, workspaceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (ledger *SQLLedger) ReceiptHistoryComplete(ctx context.Context, workspaceID string) (bool, error) {
	if err := ledger.validate(); err != nil {
		return false, err
	}
	var complete bool
	err := ledger.db.QueryRowContext(ctx, ledger.bind(`SELECT receipt_history_complete FROM mobile_v2_commit_heads WHERE workspace_id=?`), workspaceID).Scan(&complete)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return complete, err
}

type sqlLedgerQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (ledger *SQLLedger) lookup(
	ctx context.Context,
	queryer sqlLedgerQueryer,
	workspaceID string,
	originDeviceClientID string,
	commandID string,
) (Receipt, bool, error) {
	var raw []byte
	err := queryer.QueryRowContext(ctx, ledger.bind(`SELECT receipt_json FROM mobile_v2_command_receipts
		WHERE workspace_id=? AND origin_device_client_id=? AND command_id=?`), workspaceID, originDeviceClientID, commandID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	var receipt Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return Receipt{}, false, fmt.Errorf("decode mobile-v2 terminal receipt: %w", err)
	}
	return receipt, true, nil
}

func (ledger *SQLLedger) ensureAndLockHead(ctx context.Context, runner SQLRunner, workspaceID string) error {
	if ledger.dialect == SQLDialectPostgres {
		if _, err := runner.ExecContext(ctx, `INSERT INTO mobile_v2_commit_heads(workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`, workspaceID); err != nil {
			return err
		}
		var sequence uint64
		return runner.QueryRowContext(ctx, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=$1 FOR UPDATE`, workspaceID).Scan(&sequence)
	}
	if _, err := runner.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_v2_commit_heads(workspace_id) VALUES (?)`, workspaceID); err != nil {
		return err
	}
	_, err := runner.ExecContext(ctx, `UPDATE mobile_v2_commit_heads SET latest_sequence=latest_sequence WHERE workspace_id=?`, workspaceID)
	return err
}

func (ledger *SQLLedger) historyComplete(ctx context.Context, runner SQLRunner, workspaceID string) (bool, error) {
	var complete bool
	err := runner.QueryRowContext(ctx, ledger.bind(`SELECT receipt_history_complete FROM mobile_v2_commit_heads WHERE workspace_id=?`), workspaceID).Scan(&complete)
	return complete, err
}

func (ledger *SQLLedger) nextSequence(ctx context.Context, runner SQLRunner, workspaceID string) (uint64, error) {
	var sequence uint64
	err := runner.QueryRowContext(ctx, ledger.bind(`UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=?
		RETURNING latest_sequence`), workspaceID).Scan(&sequence)
	return sequence, err
}

func (ledger *SQLLedger) validate() error {
	if ledger == nil || ledger.db == nil {
		return ErrInvalidSQLDialect
	}
	return ledger.validateDialect()
}

func (ledger *SQLLedger) validateDialect() error {
	if ledger == nil || (ledger.dialect != SQLDialectSQLite && ledger.dialect != SQLDialectPostgres) {
		return ErrInvalidSQLDialect
	}
	return nil
}

func (ledger *SQLLedger) txOptions() *sql.TxOptions {
	if ledger.dialect == SQLDialectSQLite {
		return &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	return &sql.TxOptions{Isolation: sql.LevelReadCommitted}
}

func (ledger *SQLLedger) bind(query string) string {
	if ledger.dialect != SQLDialectPostgres {
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

func validateLedgerCommand(command Command) error {
	computedDigest, err := mobilev2contract.RequestDigest(command.RawEnvelope)
	if err != nil || computedDigest != command.RequestDigest {
		return ErrRequestDigestMismatch
	}
	if command.WorkspaceID == "" || command.OriginDeviceClientID == "" || command.CommandID == "" ||
		command.CommandType == "" || command.CreatedRuntimeEpoch == "" {
		return fmt.Errorf("invalid command identity")
	}
	return ValidateExpectedRevisions(command.CommandType, command.ExpectedRevisionNames)
}

func validateScopeChanges(changes []ScopeChange) error {
	seen := make(map[mobilev2sync.ScopeName]struct{}, len(changes))
	for _, change := range changes {
		switch change.Scope {
		case mobilev2sync.ScopeIPhoneContent,
			mobilev2sync.ScopeIPhoneTaskCore,
			mobilev2sync.ScopeIPhoneOccurrenceWindow,
			mobilev2sync.ScopeWatchOccurrenceWindow:
		default:
			return ErrInvalidTerminalStatus
		}
		if _, exists := seen[change.Scope]; exists {
			return ErrInvalidTerminalStatus
		}
		seen[change.Scope] = struct{}{}
		for _, image := range change.AfterImages {
			trimmed := strings.TrimSpace(string(image))
			if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(image) {
				return ErrInvalidTerminalStatus
			}
		}
	}
	return nil
}

func marshalAfterImages(images [][]byte) ([]byte, error) {
	raw := make([]json.RawMessage, len(images))
	for index, image := range images {
		if !json.Valid(image) {
			return nil, ErrInvalidTerminalStatus
		}
		raw[index] = append(json.RawMessage(nil), image...)
	}
	return json.Marshal(raw)
}

func unmarshalAfterImages(encoded []byte) ([][]byte, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return nil, err
	}
	result := make([][]byte, len(raw))
	for index, image := range raw {
		result[index] = append([]byte(nil), image...)
	}
	return result, nil
}
