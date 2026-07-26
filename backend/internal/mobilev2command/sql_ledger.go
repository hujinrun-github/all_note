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
)

type SQLDialect string

const (
	SQLDialectSQLite   SQLDialect = "sqlite"
	SQLDialectPostgres SQLDialect = "postgres"
)

var ErrInvalidSQLDialect = errors.New("mobile-v2 invalid SQL dialect")

type TerminalCommit struct {
	Command     Command
	Result      DomainResult
	CompletedAt time.Time
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
	if err := ledger.validate(); err != nil {
		return Response{}, err
	}
	if err := validateLedgerCommand(input.Command); err != nil {
		return Response{}, err
	}
	if input.Result.Status == StatusRetryLater {
		return Response{RetryLater: true}, nil
	}
	if !terminalStatus(input.Result.Status) {
		return Response{}, ErrInvalidTerminalStatus
	}
	completedAt := input.CompletedAt.UTC()
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}

	tx, err := ledger.db.BeginTx(ctx, ledger.txOptions())
	if err != nil {
		return Response{}, err
	}
	defer tx.Rollback()

	if err := ledger.ensureAndLockHead(ctx, tx, input.Command.WorkspaceID); err != nil {
		return Response{}, err
	}
	stored, found, err := ledger.lookup(ctx, tx, input.Command.WorkspaceID, input.Command.OriginDeviceClientID, input.Command.CommandID)
	if err != nil {
		return Response{}, err
	}
	if found {
		if stored.RequestDigest != input.Command.RequestDigest {
			return Response{}, ErrRequestDigestMismatch
		}
		copy := cloneReceipt(stored)
		if err := tx.Commit(); err != nil {
			return Response{}, err
		}
		return Response{Receipt: &copy, Replayed: true}, nil
	}
	complete, err := ledger.historyComplete(ctx, tx, input.Command.WorkspaceID)
	if err != nil {
		return Response{}, err
	}
	if !complete {
		return Response{}, ErrReceiptHistoryAmbiguous
	}
	if apply != nil {
		if err := apply(ctx, tx); err != nil {
			return Response{}, err
		}
	}

	sequence, err := ledger.nextSequence(ctx, tx, input.Command.WorkspaceID)
	if err != nil {
		return Response{}, err
	}
	receipt := Receipt{
		WorkspaceID: input.Command.WorkspaceID, OriginDeviceClientID: input.Command.OriginDeviceClientID,
		CommandID: input.Command.CommandID, RequestDigest: input.Command.RequestDigest, Status: input.Result.Status,
		CommitSequence: sequence, IdentityMappings: cloneMappings(input.Result.IdentityMappings),
		AffectedRevisions: cloneRevisions(input.Result.AffectedRevisions), CompletedAt: completedAt,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return Response{}, err
	}
	afterImagesJSON, err := json.Marshal(cloneImages(input.Result.AfterImages))
	if err != nil {
		return Response{}, err
	}
	if _, err := tx.ExecContext(ctx, ledger.bind(`INSERT INTO mobile_v2_command_receipts
		(workspace_id,origin_device_client_id,command_id,request_digest,command_type,status,commit_sequence,receipt_json,completed_at)
		VALUES (?,?,?,?,?,?,?,?,?)`),
		input.Command.WorkspaceID, input.Command.OriginDeviceClientID, input.Command.CommandID, input.Command.RequestDigest,
		input.Command.CommandType, input.Result.Status, sequence, string(receiptJSON), completedAt); err != nil {
		return Response{}, err
	}
	if _, err := tx.ExecContext(ctx, ledger.bind(`INSERT INTO mobile_v2_change_batches
		(workspace_id,sequence,caused_by_command_id,origin_device_client_id,receipt_json,after_images_json,committed_at)
		VALUES (?,?,?,?,?,?,?)`),
		input.Command.WorkspaceID, sequence, input.Command.CommandID, input.Command.OriginDeviceClientID,
		string(receiptJSON), string(afterImagesJSON), completedAt); err != nil {
		return Response{}, err
	}
	if err := tx.Commit(); err != nil {
		return Response{}, err
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
		if err := json.Unmarshal(afterImagesJSON, &change.AfterImages); err != nil {
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

func (ledger *SQLLedger) ensureAndLockHead(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	if ledger.dialect == SQLDialectPostgres {
		if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_v2_commit_heads(workspace_id) VALUES ($1) ON CONFLICT DO NOTHING`, workspaceID); err != nil {
			return err
		}
		var sequence uint64
		return tx.QueryRowContext(ctx, `SELECT latest_sequence FROM mobile_v2_commit_heads WHERE workspace_id=$1 FOR UPDATE`, workspaceID).Scan(&sequence)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO mobile_v2_commit_heads(workspace_id) VALUES (?)`, workspaceID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE mobile_v2_commit_heads SET latest_sequence=latest_sequence WHERE workspace_id=?`, workspaceID)
	return err
}

func (ledger *SQLLedger) historyComplete(ctx context.Context, tx *sql.Tx, workspaceID string) (bool, error) {
	var complete bool
	err := tx.QueryRowContext(ctx, ledger.bind(`SELECT receipt_history_complete FROM mobile_v2_commit_heads WHERE workspace_id=?`), workspaceID).Scan(&complete)
	return complete, err
}

func (ledger *SQLLedger) nextSequence(ctx context.Context, tx *sql.Tx, workspaceID string) (uint64, error) {
	var sequence uint64
	err := tx.QueryRowContext(ctx, ledger.bind(`UPDATE mobile_v2_commit_heads
		SET latest_sequence=latest_sequence+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=?
		RETURNING latest_sequence`), workspaceID).Scan(&sequence)
	return sequence, err
}

func (ledger *SQLLedger) validate() error {
	if ledger == nil || ledger.db == nil || (ledger.dialect != SQLDialectSQLite && ledger.dialect != SQLDialectPostgres) {
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
