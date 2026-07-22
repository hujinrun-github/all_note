package taskmigration

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ErrInvalidPostgresLegacyOutboxPlan means the SQL renderer was given
// anything other than the complete, canonical plan produced from the legacy
// manifest. Failing closed here keeps manifest identifiers as the only SQL
// identifier allowlist.
var ErrInvalidPostgresLegacyOutboxPlan = errors.New("invalid PostgreSQL legacy outbox trigger plan")

// RenderPostgresLegacyOutboxSQL renders transactional PostgreSQL trigger DDL
// from an already validated trigger plan. The returned statements contain no
// BEGIN or COMMIT, so the installer can atomically apply them inside its own
// transaction. A fresh-v2 plan deliberately renders no legacy-table SQL.
func RenderPostgresLegacyOutboxSQL(plan TriggerPlan) (string, error) {
	if err := validatePostgresLegacyOutboxPlan(plan); err != nil {
		return "", err
	}
	if plan.Mode == TaskDomainSourceFreshV2 {
		return "", nil
	}

	var builder strings.Builder
	for _, spec := range plan.Upserts {
		renderPostgresLegacyOutboxTrigger(&builder, spec)
	}
	for _, spec := range plan.Deletes {
		renderPostgresLegacyOutboxTrigger(&builder, spec)
	}
	for _, spec := range LegacyRoadmapFreezeTriggerManifest() {
		renderPostgresLegacyRoadmapFreezeTrigger(&builder, spec)
	}
	return builder.String(), nil
}

// RenderPostgresLegacyOutboxLockSQL takes one transaction-level lock across
// every legacy source before trigger replacement and baseline seeding. SHARE
// ROW EXCLUSIVE conflicts with ordinary DML's ROW EXCLUSIVE lock, so writes
// that started earlier are drained and later writes resume only after the
// complete trigger+baseline transaction commits.
func RenderPostgresLegacyOutboxLockSQL(plan TriggerPlan) (string, error) {
	if err := validatePostgresLegacyOutboxPlan(plan); err != nil {
		return "", err
	}
	if plan.Mode == TaskDomainSourceFreshV2 {
		return "", nil
	}

	manifest := LegacyOutboxManifest()
	tables := make([]string, 0, len(manifest)+3)
	for _, source := range manifest {
		tables = append(tables, quotePostgresManifestIdentifier(source.Table))
	}
	for _, table := range []string{"learning_roadmaps", "roadmap_nodes", "roadmap_edges"} {
		tables = append(tables, quotePostgresManifestIdentifier(table))
	}
	return "LOCK TABLE " + strings.Join(tables, ", ") + " IN SHARE ROW EXCLUSIVE MODE;\n\n", nil
}

// RenderPostgresLegacyOutboxBaselineSQL creates version-one ledger rows for
// source rows that existed before trigger installation. ON CONFLICT DO
// NOTHING is essential: reinstalling must preserve both a higher logical
// version and its deleted marker rather than silently moving the ledger
// backwards.
func RenderPostgresLegacyOutboxBaselineSQL(plan TriggerPlan) (string, error) {
	if err := validatePostgresLegacyOutboxPlan(plan); err != nil {
		return "", err
	}
	if plan.Mode == TaskDomainSourceFreshV2 {
		return "", nil
	}

	var builder strings.Builder
	for _, source := range LegacyOutboxManifest() {
		workspaceExpression := "source." + quotePostgresManifestIdentifier(source.WorkspaceColumn) + "::text"
		identityExpression := postgresLegacyIdentityExpression("source", source.IdentityColumns)
		builder.WriteString("INSERT INTO legacy_task_domain_entity_versions (\n")
		builder.WriteString("  workspace_id, entity_kind, entity_id, logical_version, deleted, updated_at\n")
		builder.WriteString(") SELECT\n")
		fmt.Fprintf(&builder, "  %s, '%s', %s, 1, FALSE, now()\n",
			workspaceExpression, source.EntityKind, identityExpression)
		fmt.Fprintf(&builder, "  FROM %s AS source\n", quotePostgresManifestIdentifier(source.Table))
		builder.WriteString("ON CONFLICT (workspace_id, entity_kind, entity_id) DO NOTHING;\n\n")
	}
	for _, source := range []struct {
		kind  LegacyEntityKind
		table string
	}{
		{LegacyEntityRoadmap, "learning_roadmaps"},
		{LegacyEntityRoadmapNode, "roadmap_nodes"},
		{LegacyEntityRoadmapEdge, "roadmap_edges"},
	} {
		builder.WriteString("INSERT INTO legacy_task_domain_entity_versions(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)\n")
		fmt.Fprintf(&builder, "SELECT workspace_id::text,'%s',id::text,1,FALSE,now() FROM %s\n", source.kind, quotePostgresManifestIdentifier(source.table))
		builder.WriteString("ON CONFLICT(workspace_id,entity_kind,entity_id) DO NOTHING;\n\n")
	}
	return builder.String(), nil
}

func renderPostgresLegacyRoadmapFreezeTrigger(builder *strings.Builder, spec LegacyRoadmapFreezeSpec) {
	rowName := "NEW"
	deleted := "FALSE"
	if spec.Operation == TriggerDelete {
		rowName = "OLD"
		deleted = "TRUE"
	}
	quotedName := quotePostgresManifestIdentifier(spec.Name)
	quotedTable := quotePostgresManifestIdentifier(spec.Table)
	fmt.Fprintf(builder, "CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $roadmap_freeze$\n", quotedName)
	builder.WriteString("DECLARE v_migration_state text;\nBEGIN\n")
	fmt.Fprintf(builder, "  SELECT migration_state INTO v_migration_state FROM workspace_task_domain_state WHERE workspace_id=%s.workspace_id::text FOR SHARE;\n", rowName)
	builder.WriteString("  IF NOT FOUND THEN RAISE EXCEPTION USING MESSAGE='legacy_roadmap_state_missing', ERRCODE='55000'; END IF;\n")
	builder.WriteString("  IF v_migration_state IN ('backfilling','catching_up','draining','ready','cutover') THEN\n")
	builder.WriteString("    RAISE EXCEPTION USING MESSAGE='legacy_roadmap_frozen', ERRCODE='55000';\n  END IF;\n")
	builder.WriteString("  INSERT INTO legacy_task_domain_entity_versions(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)\n")
	fmt.Fprintf(builder, "  VALUES(%s.workspace_id::text,'%s',%s.id::text,1,%s,now())\n", rowName, spec.EntityKind, rowName, deleted)
	builder.WriteString("  ON CONFLICT(workspace_id,entity_kind,entity_id) DO UPDATE SET logical_version=legacy_task_domain_entity_versions.logical_version+1,deleted=EXCLUDED.deleted,updated_at=now();\n")
	fmt.Fprintf(builder, "  RETURN %s;\nEND;\n$roadmap_freeze$;\n", rowName)
	fmt.Fprintf(builder, "DROP TRIGGER IF EXISTS %s ON %s;\n", quotedName, quotedTable)
	fmt.Fprintf(builder, "CREATE TRIGGER %s AFTER %s ON %s FOR EACH ROW EXECUTE PROCEDURE %s();\n\n", quotedName, spec.Operation, quotedTable, quotedName)
}

func validatePostgresLegacyOutboxPlan(plan TriggerPlan) error {
	switch plan.Mode {
	case TaskDomainSourceFreshV2:
		if len(plan.Upserts) != 0 || len(plan.Deletes) != 0 {
			return fmt.Errorf("%w: fresh-v2 plan contains legacy triggers", ErrInvalidPostgresLegacyOutboxPlan)
		}
		return nil
	case TaskDomainSourceLegacyWorkspace:
		manifest := LegacyOutboxManifest()
		if len(plan.Upserts) != len(manifest)*2 || len(plan.Deletes) != len(manifest) {
			return fmt.Errorf("%w: incomplete trigger set", ErrInvalidPostgresLegacyOutboxPlan)
		}
		for index, entry := range manifest {
			wantInsert := triggerSpec(entry, TriggerInsert, TriggerAfterImage)
			wantUpdate := triggerSpec(entry, TriggerUpdate, TriggerAfterImage)
			if !reflect.DeepEqual(plan.Upserts[index*2], wantInsert) || !reflect.DeepEqual(plan.Upserts[index*2+1], wantUpdate) {
				return fmt.Errorf("%w: non-canonical upsert trigger at manifest position %d", ErrInvalidPostgresLegacyOutboxPlan, index)
			}
			wantDelete := triggerSpec(manifest[len(manifest)-1-index], TriggerDelete, TriggerTombstoneBeforeImage)
			if !reflect.DeepEqual(plan.Deletes[index], wantDelete) {
				return fmt.Errorf("%w: non-canonical delete trigger at manifest position %d", ErrInvalidPostgresLegacyOutboxPlan, index)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported source mode", ErrInvalidPostgresLegacyOutboxPlan)
	}
}

func renderPostgresLegacyOutboxTrigger(builder *strings.Builder, spec TriggerSpec) {
	operationName := strings.ToLower(string(spec.Operation))
	triggerName := "task_domain_legacy_outbox_" + spec.Table + "_" + operationName
	rowName := "NEW"
	deleted := "FALSE"
	outboxOperation := "upsert"
	rowImage := postgresLegacyRowImageExpression(spec.Table, "NEW", spec.RequiredColumns)
	tombstoneImage := "NULL"
	if spec.Operation == TriggerDelete {
		rowName = "OLD"
		deleted = "TRUE"
		outboxOperation = "delete"
		rowImage = "NULL"
		tombstoneImage = postgresLegacyRowImageExpression(spec.Table, "OLD", spec.RequiredColumns)
	}

	quotedTriggerName := quotePostgresManifestIdentifier(triggerName)
	quotedTable := quotePostgresManifestIdentifier(spec.Table)
	workspaceExpression := rowName + "." + quotePostgresManifestIdentifier(spec.WorkspaceColumn) + "::text"
	identityExpression := postgresLegacyIdentityExpression(rowName, spec.IdentityColumns)

	fmt.Fprintf(builder, "CREATE OR REPLACE FUNCTION %s()\n", quotedTriggerName)
	builder.WriteString("RETURNS trigger\n")
	builder.WriteString("LANGUAGE plpgsql\n")
	builder.WriteString("AS $task_domain_outbox$\n")
	builder.WriteString("DECLARE\n")
	builder.WriteString("  v_workspace_id text;\n")
	builder.WriteString("  v_entity_id text;\n")
	builder.WriteString("  v_logical_version bigint;\n")
	builder.WriteString("  v_accept_legacy_writes boolean;\n")
	builder.WriteString("BEGIN\n")
	fmt.Fprintf(builder, "  v_workspace_id := %s;\n", workspaceExpression)
	fmt.Fprintf(builder, "  v_entity_id := %s;\n\n", identityExpression)
	builder.WriteString("  SELECT accept_legacy_writes\n")
	builder.WriteString("    INTO v_accept_legacy_writes\n")
	builder.WriteString("    FROM workspace_task_domain_state\n")
	builder.WriteString("   WHERE workspace_id = v_workspace_id\n")
	builder.WriteString("   FOR SHARE;\n")
	builder.WriteString("  IF NOT FOUND OR NOT COALESCE(v_accept_legacy_writes, FALSE) THEN\n")
	builder.WriteString("    RAISE EXCEPTION USING MESSAGE = 'legacy_task_domain_fenced', ERRCODE = '55000';\n")
	builder.WriteString("  END IF;\n\n")
	builder.WriteString("  INSERT INTO legacy_task_domain_entity_versions (\n")
	builder.WriteString("    workspace_id, entity_kind, entity_id, logical_version, deleted, updated_at\n")
	fmt.Fprintf(builder, "  ) VALUES (v_workspace_id, '%s', v_entity_id, 1, %s, now())\n", spec.EntityKind, deleted)
	builder.WriteString("  ON CONFLICT (workspace_id, entity_kind, entity_id) DO UPDATE SET\n")
	builder.WriteString("    logical_version = legacy_task_domain_entity_versions.logical_version + 1,\n")
	builder.WriteString("    deleted = EXCLUDED.deleted,\n")
	builder.WriteString("    updated_at = now()\n")
	builder.WriteString("  RETURNING logical_version INTO v_logical_version;\n\n")
	builder.WriteString("  INSERT INTO task_domain_legacy_outbox (\n")
	builder.WriteString("    workspace_id, entity_kind, entity_id, operation, source_logical_version, row_image, tombstone_image\n")
	fmt.Fprintf(builder, "  ) VALUES (v_workspace_id, '%s', v_entity_id, '%s', v_logical_version, %s, %s);\n\n",
		spec.EntityKind, outboxOperation, rowImage, tombstoneImage)
	fmt.Fprintf(builder, "  RETURN %s;\n", rowName)
	builder.WriteString("END;\n")
	builder.WriteString("$task_domain_outbox$;\n\n")
	fmt.Fprintf(builder, "DROP TRIGGER IF EXISTS %s ON %s;\n", quotedTriggerName, quotedTable)
	fmt.Fprintf(builder, "CREATE TRIGGER %s\n", quotedTriggerName)
	fmt.Fprintf(builder, "AFTER %s ON %s\n", spec.Operation, quotedTable)
	// PostgreSQL 10 (the oldest supported deployment) uses EXECUTE
	// PROCEDURE for trigger functions. Newer releases keep this spelling as a
	// compatibility alias, so it is portable across the supported range.
	builder.WriteString("FOR EACH ROW EXECUTE PROCEDURE ")
	builder.WriteString(quotedTriggerName)
	builder.WriteString("();\n\n")
}

func postgresLegacyIdentityExpression(rowName string, columns []string) string {
	if len(columns) == 1 {
		return rowName + "." + quotePostgresManifestIdentifier(columns[0]) + "::text"
	}
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = "to_jsonb(" + rowName + "." + quotePostgresManifestIdentifier(column) + ")"
	}
	return "jsonb_build_array(" + strings.Join(parts, ", ") + ")::text"
}

func postgresLegacyRowImageExpression(table, rowName string, columns []string) string {
	parts := make([]string, 0, len(columns)*2)
	for _, column := range columns {
		physical := column
		if table == "events" {
			switch column {
			case "start_time":
				physical = "start_at"
			case "end_time":
				physical = "end_at"
			}
		}
		parts = append(parts, "'"+strings.ReplaceAll(column, "'", "''")+"'", rowName+"."+quotePostgresManifestIdentifier(physical))
	}
	return "jsonb_build_object(" + strings.Join(parts, ", ") + ")"
}

// Identifiers reaching this helper have already been structurally compared
// with LegacyOutboxManifest. Quoting remains defense in depth and makes the
// emitted SQL independent of PostgreSQL keyword changes.
func quotePostgresManifestIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
