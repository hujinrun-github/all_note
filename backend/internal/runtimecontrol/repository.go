package runtimecontrol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrStateNotFound = errors.New("workspace runtime state not found")
	ErrCASConflict   = errors.New("workspace runtime state CAS conflict")
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

type State struct {
	WorkspaceID     string
	Mode            string
	Epoch           int64
	BindingRevision int64
	OperationKind   string
	OperationID     string
}

type Activation struct {
	WorkspaceID                    string
	OperationID                    string
	ExpectedEpoch                  int64
	ExpectedBindingRowRevision     int64
	ExpectedRuntimeBindingRevision int64
	BindingKind                    string
	BindingMode                    string
	EndpointSourceType             string
	EndpointID                     string
	ActorUserID                    string
}

type Repository struct {
	db      *sql.DB
	dialect Dialect
}

func New(db *sql.DB, dialect Dialect) (*Repository, error) {
	if db == nil {
		return nil, errors.New("runtime control database is nil")
	}
	if dialect != DialectPostgres && dialect != DialectSQLite {
		return nil, fmt.Errorf("unsupported runtime control dialect %q", dialect)
	}
	return &Repository{db: db, dialect: dialect}, nil
}

func (r *Repository) Get(ctx context.Context, workspaceID string) (State, error) {
	return r.get(ctx, r.db, workspaceID)
}

func (r *Repository) BeginOperation(ctx context.Context, workspaceID string, expectedBindingRevision int64, operationKind, operationID, actorUserID string) (State, error) {
	if operationKind != "migration" && operationKind != "rebind" {
		return State{}, fmt.Errorf("invalid storage operation kind %q", operationKind)
	}
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE workspace_runtime_state
		SET mode='draining', epoch=epoch+1, storage_operation_kind=?, storage_operation_id=?, updated_by=?, updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND mode='active' AND binding_revision=? AND storage_operation_id IS NULL`), operationKind, operationID, actorUserID, workspaceID, expectedBindingRevision)
	if err != nil {
		return State{}, err
	}
	if err := requireOneRow(result); err != nil {
		return State{}, err
	}
	return r.Get(ctx, workspaceID)
}

func (r *Repository) Transition(ctx context.Context, workspaceID, operationID string, expectedEpoch int64, fromMode, toMode, actorUserID string) (State, error) {
	if !validTransition(fromMode, toMode) {
		return State{}, fmt.Errorf("invalid runtime transition %s -> %s", fromMode, toMode)
	}
	result, err := r.db.ExecContext(ctx, r.bind(`UPDATE workspace_runtime_state SET mode=?,updated_by=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND mode=? AND epoch=? AND storage_operation_id=?`), toMode, actorUserID, workspaceID, fromMode, expectedEpoch, operationID)
	if err != nil {
		return State{}, err
	}
	if err := requireOneRow(result); err != nil {
		return State{}, err
	}
	return r.Get(ctx, workspaceID)
}

func (r *Repository) ActivateBinding(ctx context.Context, activation Activation) (State, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer tx.Rollback()
	var source any
	var endpoint any
	if activation.BindingMode == "disabled" || activation.BindingMode == "reuse_chat" {
		source, endpoint = nil, nil
	} else {
		source, endpoint = activation.EndpointSourceType, activation.EndpointID
	}
	result, err := tx.ExecContext(ctx, r.bind(`UPDATE workspace_service_bindings
		SET mode=?,endpoint_source_type=?,endpoint_id=?,revision=revision+1,updated_by=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND kind=? AND revision=?`), activation.BindingMode, source, endpoint, activation.ActorUserID, activation.WorkspaceID, activation.BindingKind, activation.ExpectedBindingRowRevision)
	if err != nil {
		return State{}, err
	}
	if err := requireOneRow(result); err != nil {
		return State{}, err
	}
	result, err = tx.ExecContext(ctx, r.bind(`UPDATE workspace_runtime_state
		SET mode='active',binding_revision=binding_revision+1,storage_operation_kind=NULL,storage_operation_id=NULL,updated_by=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND mode='activating' AND epoch=? AND binding_revision=? AND storage_operation_id=?`), activation.ActorUserID, activation.WorkspaceID, activation.ExpectedEpoch, activation.ExpectedRuntimeBindingRevision, activation.OperationID)
	if err != nil {
		return State{}, err
	}
	if err := requireOneRow(result); err != nil {
		return State{}, err
	}
	state, err := r.get(ctx, tx, activation.WorkspaceID)
	if err != nil {
		return State{}, err
	}
	if err := tx.Commit(); err != nil {
		return State{}, err
	}
	return state, nil
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (r *Repository) get(ctx context.Context, queryer rowQueryer, workspaceID string) (State, error) {
	var state State
	var operationKind, operationID sql.NullString
	err := queryer.QueryRowContext(ctx, r.bind(`SELECT workspace_id,mode,epoch,binding_revision,storage_operation_kind,storage_operation_id FROM workspace_runtime_state WHERE workspace_id=?`), workspaceID).
		Scan(&state.WorkspaceID, &state.Mode, &state.Epoch, &state.BindingRevision, &operationKind, &operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, ErrStateNotFound
	}
	if err != nil {
		return State{}, err
	}
	state.OperationKind = operationKind.String
	state.OperationID = operationID.String
	return state, nil
}

func (r *Repository) bind(query string) string {
	if r.dialect == DialectSQLite {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, char := range query {
		if char == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func requireOneRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrCASConflict
	}
	return nil
}

func validTransition(from, to string) bool {
	return (from == "draining" && to == "migrating") || (from == "migrating" && to == "activating")
}
