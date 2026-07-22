package taskmigration

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// ErrInvalidSQLiteLegacyOutboxPlan means the renderer was not given the
// complete canonical plan produced from LegacyOutboxManifest. Refusing the
// whole plan keeps manifest identifiers as the only source-table allowlist.
var ErrInvalidSQLiteLegacyOutboxPlan = errors.New("invalid SQLite legacy outbox trigger plan")

// RenderSQLiteLegacyOutboxSQL renders repeatable SQLite trigger DDL. The
// statements intentionally contain no transaction control: callers install
// the complete set inside one transaction. Fresh-v2 workspaces have no legacy
// sources and therefore render an empty string.
func RenderSQLiteLegacyOutboxSQL(plan TriggerPlan) (string, error) {
	if err := validateSQLiteLegacyOutboxPlan(plan); err != nil {
		return "", err
	}
	if plan.Mode == TaskDomainSourceFreshV2 {
		return "", nil
	}

	var builder strings.Builder
	for _, spec := range plan.Upserts {
		renderSQLiteLegacyOutboxTrigger(&builder, spec, plan.TaskDueColumn)
	}
	for _, spec := range plan.Deletes {
		renderSQLiteLegacyOutboxTrigger(&builder, spec, plan.TaskDueColumn)
	}
	for _, spec := range LegacyRoadmapFreezeTriggerManifest() {
		renderSQLiteLegacyRoadmapFreezeTrigger(&builder, spec)
	}
	return builder.String(), nil
}

// RenderSQLiteLegacyOutboxBaselineSQL creates logical-version ledger rows for
// source rows that predate trigger installation. The caller executes it in
// the same transaction, after installing the trigger set. SQLite's first DDL
// statement has already acquired the single-writer lock at that point, so a
// legacy write cannot slip between trigger installation and this baseline.
// Existing ledger rows are deliberately left untouched: reinstalling the
// trigger set must never reset an entity's monotonic logical version.
func RenderSQLiteLegacyOutboxBaselineSQL(plan TriggerPlan) (string, error) {
	if err := validateSQLiteLegacyOutboxPlan(plan); err != nil {
		return "", err
	}
	if plan.Mode == TaskDomainSourceFreshV2 {
		return "", nil
	}

	var builder strings.Builder
	for _, source := range LegacyOutboxManifest() {
		workspaceExpression := "CAST(source." + quoteSQLiteManifestIdentifier(source.WorkspaceColumn) + " AS TEXT)"
		identityExpression := sqliteLegacyIdentityExpression("source", source.IdentityColumns)
		builder.WriteString("INSERT INTO legacy_task_domain_entity_versions (\n")
		builder.WriteString("  workspace_id, entity_kind, entity_id, logical_version, deleted, updated_at\n")
		builder.WriteString(") SELECT\n")
		fmt.Fprintf(&builder, "  %s, '%s', %s, 1, 0, CURRENT_TIMESTAMP\n",
			workspaceExpression, source.EntityKind, identityExpression)
		fmt.Fprintf(&builder, "  FROM %s AS source\n", quoteSQLiteManifestIdentifier(source.Table))
		// The always-true WHERE removes SQLite's INSERT..SELECT UPSERT parser
		// ambiguity without changing the selected baseline rows.
		builder.WriteString(" WHERE 1\n")
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
		builder.WriteString("INSERT INTO legacy_task_domain_entity_versions (workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)\n")
		fmt.Fprintf(&builder, "SELECT CAST(workspace_id AS TEXT),'%s',CAST(id AS TEXT),1,0,CURRENT_TIMESTAMP FROM %s WHERE 1\n", source.kind, quoteSQLiteManifestIdentifier(source.table))
		builder.WriteString("ON CONFLICT (workspace_id,entity_kind,entity_id) DO NOTHING;\n\n")
	}
	return builder.String(), nil
}

func renderSQLiteLegacyRoadmapFreezeTrigger(builder *strings.Builder, spec LegacyRoadmapFreezeSpec) {
	rowName := "NEW"
	deleted := "0"
	if spec.Operation == TriggerDelete {
		rowName = "OLD"
		deleted = "1"
	}
	workspace := "CAST(" + rowName + ".\"workspace_id\" AS TEXT)"
	identity := "CAST(" + rowName + ".\"id\" AS TEXT)"
	fmt.Fprintf(builder, "DROP TRIGGER IF EXISTS %s;\n", quoteSQLiteManifestIdentifier(spec.Name))
	fmt.Fprintf(builder, "CREATE TRIGGER %s AFTER %s ON %s FOR EACH ROW BEGIN\n", quoteSQLiteManifestIdentifier(spec.Name), spec.Operation, quoteSQLiteManifestIdentifier(spec.Table))
	builder.WriteString("  SELECT RAISE(ABORT,'legacy_roadmap_frozen') WHERE EXISTS (SELECT 1 FROM workspace_task_domain_state\n")
	fmt.Fprintf(builder, "    WHERE workspace_id=%s AND migration_state IN ('backfilling','catching_up','draining','ready','cutover'));\n", workspace)
	builder.WriteString("  INSERT INTO legacy_task_domain_entity_versions(workspace_id,entity_kind,entity_id,logical_version,deleted,updated_at)\n")
	fmt.Fprintf(builder, "  VALUES(%s,'%s',%s,1,%s,CURRENT_TIMESTAMP)\n", workspace, spec.EntityKind, identity, deleted)
	builder.WriteString("  ON CONFLICT(workspace_id,entity_kind,entity_id) DO UPDATE SET logical_version=logical_version+1,deleted=excluded.deleted,updated_at=CURRENT_TIMESTAMP;\n")
	builder.WriteString("END;\n\n")
}

func validateSQLiteLegacyOutboxPlan(plan TriggerPlan) error {
	switch plan.Mode {
	case TaskDomainSourceFreshV2:
		if len(plan.Upserts) != 0 || len(plan.Deletes) != 0 {
			return fmt.Errorf("%w: fresh-v2 plan contains legacy triggers", ErrInvalidSQLiteLegacyOutboxPlan)
		}
		return nil
	case TaskDomainSourceLegacyWorkspace:
		manifest := LegacyOutboxManifest()
		if len(plan.Upserts) != len(manifest)*2 || len(plan.Deletes) != len(manifest) {
			return fmt.Errorf("%w: incomplete trigger set", ErrInvalidSQLiteLegacyOutboxPlan)
		}
		for index, entry := range manifest {
			wantInsert := triggerSpec(entry, TriggerInsert, TriggerAfterImage)
			wantUpdate := triggerSpec(entry, TriggerUpdate, TriggerAfterImage)
			if !reflect.DeepEqual(plan.Upserts[index*2], wantInsert) || !reflect.DeepEqual(plan.Upserts[index*2+1], wantUpdate) {
				return fmt.Errorf("%w: non-canonical upsert trigger at manifest position %d", ErrInvalidSQLiteLegacyOutboxPlan, index)
			}
			wantDelete := triggerSpec(manifest[len(manifest)-1-index], TriggerDelete, TriggerTombstoneBeforeImage)
			if !reflect.DeepEqual(plan.Deletes[index], wantDelete) {
				return fmt.Errorf("%w: non-canonical delete trigger at manifest position %d", ErrInvalidSQLiteLegacyOutboxPlan, index)
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported source mode", ErrInvalidSQLiteLegacyOutboxPlan)
	}
}

func renderSQLiteLegacyOutboxTrigger(builder *strings.Builder, spec TriggerSpec, taskDueColumn string) {
	operationName := strings.ToLower(string(spec.Operation))
	triggerName := "task_domain_legacy_outbox_" + spec.Table + "_" + operationName
	rowName := "NEW"
	deleted := "0"
	outboxOperation := "upsert"
	rowImage := sqliteLegacyRowImageExpression(spec.Table, rowName, spec.RequiredColumns, taskDueColumn)
	tombstoneImage := "NULL"
	if spec.Operation == TriggerDelete {
		rowName = "OLD"
		deleted = "1"
		outboxOperation = "delete"
		rowImage = "NULL"
		tombstoneImage = sqliteLegacyRowImageExpression(spec.Table, rowName, spec.RequiredColumns, taskDueColumn)
	}

	quotedTriggerName := quoteSQLiteManifestIdentifier(triggerName)
	quotedTable := quoteSQLiteManifestIdentifier(spec.Table)
	workspaceExpression := "CAST(" + rowName + "." + quoteSQLiteManifestIdentifier(spec.WorkspaceColumn) + " AS TEXT)"
	identityExpression := sqliteLegacyIdentityExpression(rowName, spec.IdentityColumns)

	fmt.Fprintf(builder, "DROP TRIGGER IF EXISTS %s;\n", quotedTriggerName)
	fmt.Fprintf(builder, "CREATE TRIGGER %s\n", quotedTriggerName)
	fmt.Fprintf(builder, "AFTER %s ON %s\n", spec.Operation, quotedTable)
	builder.WriteString("FOR EACH ROW\n")
	builder.WriteString("BEGIN\n")
	builder.WriteString("  SELECT RAISE(ABORT, 'legacy_task_domain_fenced')\n")
	builder.WriteString("   WHERE NOT EXISTS (\n")
	builder.WriteString("    SELECT 1\n")
	builder.WriteString("      FROM workspace_task_domain_state\n")
	fmt.Fprintf(builder, "     WHERE workspace_id = %s\n", workspaceExpression)
	builder.WriteString("       AND accept_legacy_writes = 1\n")
	builder.WriteString("  );\n\n")
	builder.WriteString("  INSERT INTO legacy_task_domain_entity_versions (\n")
	builder.WriteString("    workspace_id, entity_kind, entity_id, logical_version, deleted, updated_at\n")
	fmt.Fprintf(builder, "  ) VALUES (%s, '%s', %s, 1, %s, CURRENT_TIMESTAMP)\n",
		workspaceExpression, spec.EntityKind, identityExpression, deleted)
	builder.WriteString("  ON CONFLICT (workspace_id, entity_kind, entity_id) DO UPDATE SET\n")
	builder.WriteString("    logical_version = legacy_task_domain_entity_versions.logical_version + 1,\n")
	builder.WriteString("    deleted = excluded.deleted,\n")
	builder.WriteString("    updated_at = CURRENT_TIMESTAMP;\n\n")
	builder.WriteString("  INSERT INTO task_domain_legacy_outbox (\n")
	builder.WriteString("    workspace_id, entity_kind, entity_id, operation, source_logical_version, row_image, tombstone_image\n")
	builder.WriteString("  ) SELECT\n")
	fmt.Fprintf(builder, "    %s, '%s', %s, '%s', logical_version, %s, %s\n",
		workspaceExpression, spec.EntityKind, identityExpression, outboxOperation, rowImage, tombstoneImage)
	builder.WriteString("    FROM legacy_task_domain_entity_versions\n")
	fmt.Fprintf(builder, "   WHERE workspace_id = %s\n", workspaceExpression)
	fmt.Fprintf(builder, "     AND entity_kind = '%s'\n", spec.EntityKind)
	fmt.Fprintf(builder, "     AND entity_id = %s;\n", identityExpression)
	builder.WriteString("END;\n\n")
}

func sqliteLegacyIdentityExpression(rowName string, columns []string) string {
	if len(columns) == 1 {
		return "CAST(" + rowName + "." + quoteSQLiteManifestIdentifier(columns[0]) + " AS TEXT)"
	}
	parts := make([]string, len(columns))
	for index, column := range columns {
		parts[index] = "CAST(" + rowName + "." + quoteSQLiteManifestIdentifier(column) + " AS TEXT)"
	}
	return "json_array(" + strings.Join(parts, ", ") + ")"
}

func sqliteLegacyRowImageExpression(table, rowName string, columns []string, taskDueColumn string) string {
	parts := make([]string, 0, len(columns)*2)
	for _, column := range columns {
		physicalColumn := column
		if table == "tasks" && column == "due_at" {
			physicalColumn = taskDueColumn
		}
		parts = append(parts, quoteSQLiteStringLiteral(column), rowName+"."+quoteSQLiteManifestIdentifier(physicalColumn))
	}
	return "json_object(" + strings.Join(parts, ", ") + ")"
}

// Source identifiers and image keys are accepted only after the whole plan is
// structurally compared with LegacyOutboxManifest. Quoting remains defense in
// depth and makes generated DDL safe across SQLite keyword changes.
func quoteSQLiteManifestIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteSQLiteStringLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
