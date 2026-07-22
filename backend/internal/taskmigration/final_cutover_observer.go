package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var (
	ErrInvalidFinalCutoverObserver = errors.New("invalid final cutover observer")
	ErrFinalCutoverObservation     = errors.New("final cutover observation failed closed")
)

// DBFinalCutoverObserverConfig contains only provider and timezone fallbacks.
// The migration timezone persisted in workspace_task_domain_state remains the
// authoritative interpretation of the frozen legacy rows.
type DBFinalCutoverObserverConfig struct {
	DB                 *sql.DB
	Dialect            Dialect
	OwnerTimezone      string
	DeploymentTimezone string
}

// DBFinalCutoverObserver captures the last source/v2 comparison through one
// serializable, read-only database snapshot. It does not repair data, advance
// migration state, or perform cutover.
type DBFinalCutoverObserver struct {
	db                 *sql.DB
	dialect            Dialect
	ownerTimezone      string
	deploymentTimezone string
	reconcile          *ReconcileStore
}

var _ FinalCutoverObserver = (*DBFinalCutoverObserver)(nil)

func NewDBFinalCutoverObserver(config DBFinalCutoverObserverConfig) (*DBFinalCutoverObserver, error) {
	if config.DB == nil || (config.Dialect != DialectSQLite && config.Dialect != DialectPostgres) {
		return nil, ErrInvalidFinalCutoverObserver
	}
	reconcile, err := NewReconcileStore(config.DB, config.Dialect)
	if err != nil {
		return nil, fmt.Errorf("%w: construct reconcile store: %v", ErrInvalidFinalCutoverObserver, err)
	}
	return &DBFinalCutoverObserver{
		db: config.DB, dialect: config.Dialect,
		ownerTimezone:      strings.TrimSpace(config.OwnerTimezone),
		deploymentTimezone: strings.TrimSpace(config.DeploymentTimezone),
		reconcile:          reconcile,
	}, nil
}

func (o *DBFinalCutoverObserver) ObserveFinalCutover(
	ctx context.Context,
	workspaceID string,
	migrationID string,
	cutoverRevision uint64,
) (FinalCutoverObservation, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	migrationID = strings.TrimSpace(migrationID)
	if o == nil || o.db == nil || o.reconcile == nil || ctx == nil || workspaceID == "" || migrationID == "" || cutoverRevision > math.MaxInt64 {
		return FinalCutoverObservation{}, ErrInvalidFinalCutoverObserver
	}

	tx, err := o.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("begin read-only snapshot", err)
	}
	defer func() { _ = tx.Rollback() }()

	state, err := o.loadAndValidateFence(ctx, tx, workspaceID, migrationID, cutoverRevision)
	if err != nil {
		return FinalCutoverObservation{}, err
	}
	if err := o.validateCanonicalTriggers(ctx, tx); err != nil {
		return FinalCutoverObservation{}, err
	}

	outboxWatermark, pendingOutbox, err := o.observeOutbox(ctx, tx, state)
	if err != nil {
		return FinalCutoverObservation{}, err
	}

	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{
		Dialect: o.dialect, WorkspaceID: workspaceID,
		// The value frozen when migration started is authoritative. Passing it
		// as the highest-priority source makes the final mapping independent of
		// later user/profile timezone changes.
		WorkspaceTimezone: state.MigrationTimezone,
		OwnerTimezone:     o.ownerTimezone, DeploymentTimezone: o.deploymentTimezone,
	})
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("construct legacy snapshot loader", err)
	}
	inventory, rows, err := loader.Load(ctx, tx)
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("load frozen legacy source", err)
	}
	preflight, err := PreflightLegacyTaskDomain(inventory)
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("preflight frozen legacy source", err)
	}
	if preflight.MigrationTimezone != state.MigrationTimezone {
		return FinalCutoverObservation{}, o.observationError("validate frozen migration timezone", errors.New("preflight timezone changed"))
	}
	projection, err := MapLegacyTaskDomain(preflight, rows)
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("map frozen legacy source", err)
	}
	sourceVersions, err := o.loadCompleteSourceVersions(ctx, tx, workspaceID, projection)
	if err != nil {
		return FinalCutoverObservation{}, err
	}
	reconciled, err := o.reconcile.observeTx(ctx, tx, workspaceID, projection, sourceVersions)
	if err != nil {
		return FinalCutoverObservation{}, o.observationError("reconcile frozen source and v2", err)
	}

	if err := tx.Commit(); err != nil {
		return FinalCutoverObservation{}, o.observationError("commit read-only snapshot", err)
	}

	// ActiveLegacyTransactions is deliberately not a process counter. The
	// drain transaction took the tenant anchor and state fence locks, waited
	// for every already-admitted trigger transaction, closed the durable
	// fence, and advanced the epoch. With the canonical triggers still
	// installed and both durable epoch copies matching below, no legacy write
	// transaction can remain admitted after that transaction commits.
	pendingMutations := pendingOutbox + len(reconciled.Plan.UpsertMissing) + len(reconciled.Plan.DeleteExtra)
	return FinalCutoverObservation{
		OutboxWatermark:          outboxWatermark,
		ActiveLegacyTransactions: 0,
		PreviousFenceEpoch:       state.WriteEpoch - 1,
		Reconcile:                reconciled.Plan,
		PendingMutations:         pendingMutations,
	}, nil
}

func (o *DBFinalCutoverObserver) loadAndValidateFence(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	migrationID string,
	cutoverRevision uint64,
) (WorkspaceTaskDomainState, error) {
	stateQuery := `SELECT workspace_id,model_version,migration_state,source_watermark,cutover_revision,
		write_epoch,accept_legacy_writes,migration_timezone,v2_first_write_at,migration_id,last_error,revision
		FROM workspace_task_domain_state WHERE workspace_id=` + o.placeholder(1)
	state, err := scanWorkspaceTaskDomainState(tx.QueryRowContext(ctx, stateQuery, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceTaskDomainState{}, o.observationError("load task-domain fence", errors.New("workspace state is missing"))
	}
	if err != nil {
		return WorkspaceTaskDomainState{}, o.observationError("load task-domain fence", err)
	}
	if err := state.Validate(); err != nil {
		return WorkspaceTaskDomainState{}, o.observationError("validate task-domain fence", err)
	}
	// Draining is accepted so the migration coordinator can run the same final
	// observation before MarkReady and discover replay lag or safe reconcile
	// repairs. CutoverService independently requires Ready before it invokes
	// this observer for the CAS cutover gate.
	if state.WorkspaceID != workspaceID || state.ModelVersion != ModelVersionLegacy ||
		(state.MigrationState != MigrationStateDraining && state.MigrationState != MigrationStateReady) ||
		state.AcceptLegacyWrites || state.MigrationID != migrationID || state.CutoverRevision == nil ||
		*state.CutoverRevision != cutoverRevision || state.WriteEpoch <= 1 {
		return WorkspaceTaskDomainState{}, o.observationError("validate task-domain fence", errors.New("state is not the requested frozen migration"))
	}

	anchorQuery := `SELECT epoch,state,migration_id FROM tenant_workspaces WHERE workspace_id=` + o.placeholder(1)
	var (
		anchorEpoch     int64
		anchorState     string
		anchorMigration sql.NullString
	)
	if err := tx.QueryRowContext(ctx, anchorQuery, workspaceID).Scan(&anchorEpoch, &anchorState, &anchorMigration); errors.Is(err, sql.ErrNoRows) {
		return WorkspaceTaskDomainState{}, o.observationError("load tenant fence anchor", errors.New("tenant anchor is missing"))
	} else if err != nil {
		return WorkspaceTaskDomainState{}, o.observationError("load tenant fence anchor", err)
	}
	if anchorEpoch <= 0 || uint64(anchorEpoch) != state.WriteEpoch || anchorState != "active" || anchorMigration.Valid {
		return WorkspaceTaskDomainState{}, o.observationError("validate tenant fence anchor", errors.New("tenant epoch or state does not match task-domain fence"))
	}
	return state, nil
}

func (o *DBFinalCutoverObserver) observeOutbox(
	ctx context.Context,
	tx *sql.Tx,
	state WorkspaceTaskDomainState,
) (uint64, int, error) {
	if state.CutoverRevision == nil || state.SourceWatermark > math.MaxInt64 || *state.CutoverRevision > math.MaxInt64 {
		return 0, 0, o.observationError("validate outbox bounds", errors.New("outbox revision exceeds database integer range"))
	}
	query := `SELECT COALESCE(MAX(sequence),0),
		COALESCE(SUM(CASE WHEN sequence>` + o.placeholder(1) + ` AND sequence<=` + o.placeholder(2) + ` THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN sequence>` + o.placeholder(3) + ` THEN 1 ELSE 0 END),0)
		FROM task_domain_legacy_outbox WHERE workspace_id=` + o.placeholder(4)
	var maxSequence, pending, afterCutover int64
	if err := tx.QueryRowContext(ctx, query,
		state.SourceWatermark, *state.CutoverRevision, *state.CutoverRevision, state.WorkspaceID,
	).Scan(&maxSequence, &pending, &afterCutover); err != nil {
		return 0, 0, o.observationError("observe legacy outbox", err)
	}
	if maxSequence < 0 || pending < 0 || afterCutover != 0 || uint64(maxSequence) != *state.CutoverRevision || state.SourceWatermark > *state.CutoverRevision {
		return 0, 0, o.observationError("validate legacy outbox", errors.New("cutover revision, watermark, and durable outbox disagree"))
	}
	return state.SourceWatermark, int(pending), nil
}

type finalCutoverLedgerRow struct {
	key     projectionSourceKey
	version int64
	deleted bool
}

func (o *DBFinalCutoverObserver) loadCompleteSourceVersions(
	ctx context.Context,
	tx *sql.Tx,
	workspaceID string,
	projection V2Projection,
) ([]ProjectionSourceVersion, error) {
	expected := make(map[projectionSourceKey]struct{}, len(projection.IDMap))
	for _, entry := range projection.IDMap {
		key := projectionSourceKey{kind: entry.LegacyKind, id: strings.TrimSpace(entry.LegacyID)}
		if !validLegacyProjectionKind(key.kind) || key.id == "" {
			return nil, o.observationError("validate mapped source inventory", fmt.Errorf("invalid source %s/%q", key.kind, key.id))
		}
		if _, duplicate := expected[key]; duplicate {
			return nil, o.observationError("validate mapped source inventory", fmt.Errorf("duplicate source %s", projectionSourceReference(key)))
		}
		expected[key] = struct{}{}
	}

	query := `SELECT entity_kind,entity_id,logical_version,deleted
		FROM legacy_task_domain_entity_versions WHERE workspace_id=` + o.placeholder(1) + ` ORDER BY entity_kind,entity_id`
	rows, err := tx.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, o.observationError("read durable source-version ledger", err)
	}
	defer rows.Close()
	ledger := make(map[projectionSourceKey]finalCutoverLedgerRow)
	for rows.Next() {
		var kind, id string
		var version int64
		var deleted bool
		if err := rows.Scan(&kind, &id, &version, &deleted); err != nil {
			return nil, o.observationError("scan durable source-version ledger", err)
		}
		key := projectionSourceKey{kind: LegacyEntityKind(kind), id: strings.TrimSpace(id)}
		if !validLegacyProjectionKind(key.kind) || key.id == "" || version <= 0 {
			return nil, o.observationError("validate durable source-version ledger", fmt.Errorf("invalid source %s/%q version=%d", key.kind, key.id, version))
		}
		if _, duplicate := ledger[key]; duplicate {
			return nil, o.observationError("validate durable source-version ledger", fmt.Errorf("duplicate source %s", projectionSourceReference(key)))
		}
		ledger[key] = finalCutoverLedgerRow{key: key, version: version, deleted: deleted}
	}
	if err := rows.Err(); err != nil {
		return nil, o.observationError("iterate durable source-version ledger", err)
	}

	versions := make([]ProjectionSourceVersion, 0, len(expected))
	for key := range expected {
		row, ok := ledger[key]
		if !ok || row.deleted {
			return nil, o.observationError("validate durable source-version ledger", fmt.Errorf("current source %s is missing or tombstoned", projectionSourceReference(key)))
		}
		versions = append(versions, ProjectionSourceVersion{EntityKind: key.kind, LegacyID: key.id, LogicalVersion: row.version})
	}
	for key, row := range ledger {
		if row.deleted {
			continue
		}
		if _, ok := expected[key]; !ok {
			return nil, o.observationError("validate durable source-version ledger", fmt.Errorf("live ledger source %s is absent from frozen source", projectionSourceReference(key)))
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].EntityKind != versions[j].EntityKind {
			return versions[i].EntityKind < versions[j].EntityKind
		}
		return versions[i].LegacyID < versions[j].LegacyID
	})
	return versions, nil
}

func (o *DBFinalCutoverObserver) validateCanonicalTriggers(ctx context.Context, tx *sql.Tx) error {
	freezeManifest := LegacyRoadmapFreezeTriggerManifest()
	expected := make(map[string]string, len(legacyOutboxManifest)*3+len(freezeManifest))
	for _, source := range LegacyOutboxManifest() {
		for _, operation := range []string{"insert", "update", "delete"} {
			expected["task_domain_legacy_outbox_"+source.Table+"_"+operation] = source.Table
		}
	}
	// Roadmap aggregates are deliberately frozen for the whole migration
	// instead of being replayed through the five-entity outbox. Their guards
	// are therefore part of the same final fence proof, despite using a
	// distinct prefix and not contributing to the outbox watermark.
	for _, trigger := range freezeManifest {
		expected[trigger.Name] = trigger.Table
	}

	var (
		rows *sql.Rows
		err  error
	)
	if o.dialect == DialectPostgres {
		rows, err = tx.QueryContext(ctx, `SELECT t.tgname,c.relname
			FROM pg_trigger t
			JOIN pg_class c ON c.oid=t.tgrelid
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE NOT t.tgisinternal AND n.nspname=current_schema()
			AND (t.tgname LIKE 'task_domain_legacy_outbox_%'
				OR t.tgname LIKE 'task_domain_legacy_roadmap_freeze_%')
			ORDER BY t.tgname`)
	} else {
		rows, err = tx.QueryContext(ctx, `SELECT name,tbl_name FROM sqlite_master
			WHERE type='trigger' AND (name LIKE 'task_domain_legacy_outbox_%'
				OR name LIKE 'task_domain_legacy_roadmap_freeze_%') ORDER BY name`)
	}
	if err != nil {
		return o.observationError("inventory legacy outbox triggers", err)
	}
	defer rows.Close()
	seen := make(map[string]string, len(expected))
	for rows.Next() {
		var name, table string
		if err := rows.Scan(&name, &table); err != nil {
			return o.observationError("scan legacy outbox trigger inventory", err)
		}
		seen[name] = table
	}
	if err := rows.Err(); err != nil {
		return o.observationError("iterate legacy outbox trigger inventory", err)
	}
	if len(seen) != len(expected) {
		return o.observationError("validate legacy outbox trigger inventory", fmt.Errorf("installed=%d expected=%d", len(seen), len(expected)))
	}
	for name, table := range expected {
		if seen[name] != table {
			return o.observationError("validate legacy outbox trigger inventory", fmt.Errorf("trigger %s is missing or attached to the wrong table", name))
		}
	}
	return nil
}

func (o *DBFinalCutoverObserver) placeholder(position int) string {
	if o.dialect == DialectPostgres {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func (o *DBFinalCutoverObserver) observationError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrFinalCutoverObservation, operation, err)
}
