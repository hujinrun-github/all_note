package tenantruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type ControlDialect string

const (
	ControlSQLite   ControlDialect = "sqlite"
	ControlPostgres ControlDialect = "postgres"
)

// ControlSource reads only the persistent runtime version and immutable
// endpoint identities. Credentials and provider resources are resolved by the
// runtime Factory, so plaintext secrets never enter Resolver's snapshot cache.
type ControlSource struct {
	db      *sql.DB
	dialect ControlDialect
}

func NewControlSource(db *sql.DB, dialect ControlDialect) (*ControlSource, error) {
	if db == nil {
		return nil, errors.New("tenant runtime control database is required")
	}
	if dialect != ControlSQLite && dialect != ControlPostgres {
		return nil, fmt.Errorf("unsupported tenant runtime control dialect %q", dialect)
	}
	return &ControlSource{db: db, dialect: dialect}, nil
}

func (source *ControlSource) LoadVersion(ctx context.Context, workspaceID string) (Version, error) {
	if source == nil || source.db == nil || strings.TrimSpace(workspaceID) == "" {
		return Version{}, fmt.Errorf("%w: runtime version input is invalid", ErrRuntimeUnavailable)
	}
	return source.loadVersion(ctx, source.db, workspaceID)
}

func (source *ControlSource) LoadSnapshot(ctx context.Context, expected Version) (Snapshot, error) {
	if source == nil || source.db == nil || strings.TrimSpace(expected.WorkspaceID) == "" ||
		expected.Epoch < 1 || expected.BindingRevision < 1 {
		return Snapshot{}, fmt.Errorf("%w: runtime snapshot input is invalid", ErrRuntimeUnavailable)
	}
	tx, err := source.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: begin control snapshot: %v", ErrRuntimeUnavailable, err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, source.bind(`SELECT kind,mode,endpoint_id
		FROM workspace_service_bindings WHERE workspace_id=?`), expected.WorkspaceID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: load service bindings: %v", ErrRuntimeUnavailable, err)
	}
	bindings := make(map[string]runtimeBinding, 4)
	for rows.Next() {
		var binding runtimeBinding
		var endpoint sql.NullString
		if err := rows.Scan(&binding.kind, &binding.mode, &endpoint); err != nil {
			_ = rows.Close()
			return Snapshot{}, fmt.Errorf("%w: scan service binding: %v", ErrRuntimeUnavailable, err)
		}
		binding.endpointID = endpoint.String
		if _, duplicate := bindings[binding.kind]; duplicate {
			_ = rows.Close()
			return Snapshot{}, fmt.Errorf("%w: duplicate %s binding", ErrRuntimeUnavailable, binding.kind)
		}
		bindings[binding.kind] = binding
	}
	if err := rows.Close(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: close service bindings: %v", ErrRuntimeUnavailable, err)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: read service bindings: %v", ErrRuntimeUnavailable, err)
	}
	if err := validateRuntimeBindings(bindings); err != nil {
		return Snapshot{}, err
	}

	current, err := source.loadVersion(ctx, tx, expected.WorkspaceID)
	if err != nil {
		return Snapshot{}, err
	}
	if !sameVersion(current, expected) {
		return Snapshot{}, fmt.Errorf("%w: runtime version changed while loading bindings", ErrRuntimeUnavailable)
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: commit control snapshot: %v", ErrRuntimeUnavailable, err)
	}
	return Snapshot{
		Version:                 expected,
		DatabaseEndpointID:      bindings["data_store"].endpointID,
		ObjectEndpointID:        bindings["object_s3"].endpointID,
		ChatMode:                bindings["llm_chat"].mode,
		ChatEndpointID:          bindings["llm_chat"].endpointID,
		TranscriptionMode:       bindings["llm_transcription"].mode,
		TranscriptionEndpointID: bindings["llm_transcription"].endpointID,
	}, nil
}

type controlVersionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (source *ControlSource) loadVersion(ctx context.Context, queryer controlVersionQueryer, workspaceID string) (Version, error) {
	var version Version
	err := queryer.QueryRowContext(ctx, source.bind(`SELECT workspace_id,mode,epoch,binding_revision
		FROM workspace_runtime_state WHERE workspace_id=?`), workspaceID).
		Scan(&version.WorkspaceID, &version.Mode, &version.Epoch, &version.BindingRevision)
	if err != nil {
		return Version{}, fmt.Errorf("%w: load runtime version: %v", ErrRuntimeUnavailable, err)
	}
	return version, nil
}

type runtimeBinding struct {
	kind       string
	mode       string
	endpointID string
}

func validateRuntimeBindings(bindings map[string]runtimeBinding) error {
	if len(bindings) != 4 {
		return fmt.Errorf("%w: all four service bindings are required", ErrRuntimeUnavailable)
	}
	for _, kind := range []string{"data_store", "object_s3", "llm_chat", "llm_transcription"} {
		binding, ok := bindings[kind]
		if !ok {
			return fmt.Errorf("%w: %s binding is missing", ErrRuntimeUnavailable, kind)
		}
		hasEndpoint := strings.TrimSpace(binding.endpointID) != ""
		switch kind {
		case "data_store", "object_s3":
			if (binding.mode != "default" && binding.mode != "custom") || !hasEndpoint {
				return fmt.Errorf("%w: invalid %s binding", ErrRuntimeUnavailable, kind)
			}
		case "llm_chat":
			if !((binding.mode == "default" || binding.mode == "custom") && hasEndpoint) &&
				!(binding.mode == "disabled" && !hasEndpoint) {
				return fmt.Errorf("%w: invalid llm_chat binding", ErrRuntimeUnavailable)
			}
		case "llm_transcription":
			if !((binding.mode == "default" || binding.mode == "custom") && hasEndpoint) &&
				!((binding.mode == "disabled" || binding.mode == "reuse_chat") && !hasEndpoint) {
				return fmt.Errorf("%w: invalid llm_transcription binding", ErrRuntimeUnavailable)
			}
		}
	}
	return nil
}

func (source *ControlSource) bind(query string) string {
	if source.dialect == ControlSQLite {
		return query
	}
	var builder strings.Builder
	index := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&builder, "$%d", index)
			index++
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

var _ Source = (*ControlSource)(nil)
