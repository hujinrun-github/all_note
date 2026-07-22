package taskmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var ErrInvalidReplayProjectionInput = errors.New("invalid replay projection input")

type ReplayProjectionConflictCode string

const (
	ReplayProjectionConflictMissingMapping ReplayProjectionConflictCode = "missing_mapping"
	ReplayProjectionConflictMapping        ReplayProjectionConflictCode = "inconsistent_mapping"
	ReplayProjectionConflictVersion        ReplayProjectionConflictCode = "logical_version_conflict"
	ReplayProjectionConflictSourceLedger   ReplayProjectionConflictCode = "source_ledger_conflict"
	ReplayProjectionConflictWorkspace      ReplayProjectionConflictCode = "workspace_mismatch"
	ReplayProjectionConflictIdentity       ReplayProjectionConflictCode = "source_identity_mismatch"
	ReplayProjectionConflictDependency     ReplayProjectionConflictCode = "missing_dependency"
	ReplayProjectionConflictTarget         ReplayProjectionConflictCode = "target_data_conflict"
)

// ReplayProjectionConflictError is stable across providers. Migration
// orchestration must stop and reconcile these conflicts instead of guessing
// whether a provider error is safe to retry.
type ReplayProjectionConflictError struct {
	Code        ReplayProjectionConflictCode
	WorkspaceID string
	Reference   string
	Detail      string
}

func (e *ReplayProjectionConflictError) Error() string {
	message := fmt.Sprintf("task domain replay projection conflict: workspace=%s code=%s reference=%s", e.WorkspaceID, e.Code, e.Reference)
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

func (e *ReplayProjectionConflictError) Is(target error) bool {
	other, ok := target.(*ReplayProjectionConflictError)
	return ok && e.Code == other.Code
}

// ReplayProjectionApplier is an explicitly migration-scoped ReplayProjector.
// It never opens or commits a transaction: ReplayStore owns the atomic
// projection+watermark boundary. The workspace is constructor-bound because
// ReplayEvent intentionally contains no tenant identity outside its image.
type ReplayProjectionApplier struct {
	workspaceID string
	dialect     Dialect
}

func NewReplayProjectionApplier(workspaceID string, dialect Dialect) (*ReplayProjectionApplier, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || (dialect != DialectSQLite && dialect != DialectPostgres) {
		return nil, fmt.Errorf("%w: workspace and supported dialect are required", ErrInvalidReplayProjectionInput)
	}
	return &ReplayProjectionApplier{workspaceID: workspaceID, dialect: dialect}, nil
}

// Projector exposes Apply with the callback type accepted by ReplayStore.
func (a *ReplayProjectionApplier) Projector() ReplayProjector {
	if a == nil {
		return nil
	}
	return a.Apply
}

// Apply validates a complete fetched page, coalesces each source to the last
// image in that page, orders writes by domain dependency, and mutates v2 rows
// plus every ID-map target. Missing mappings fail closed: assigning target IDs
// for post-snapshot inserts requires the deterministic preflight mapper and is
// deliberately not approximated here.
func (a *ReplayProjectionApplier) Apply(ctx context.Context, tx *sql.Tx, events []ReplayEvent) error {
	if a == nil || ctx == nil || tx == nil || a.workspaceID == "" || len(events) == 0 {
		return fmt.Errorf("%w: applier, context, transaction, and events are required", ErrInvalidReplayProjectionInput)
	}
	if err := validateReplayEvents(0, events); err != nil {
		return err
	}
	normalized, err := a.normalizeEvents(events)
	if err != nil {
		return err
	}
	for _, event := range normalized {
		if err := a.applyEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return nil
}

func (a *ReplayProjectionApplier) normalizeEvents(events []ReplayEvent) ([]ReplayEvent, error) {
	latest := make(map[ReplayEntityKey]ReplayEvent, len(events))
	for _, event := range events {
		image := event.AfterImage
		if event.Operation == ReplayDelete {
			image = event.TombstoneImage
		}
		if err := a.validateImageIdentity(event, image); err != nil {
			return nil, err
		}
		key := ReplayEntityKey{Kind: event.EntityKind, SourceID: event.SourceID}
		if previous, ok := latest[key]; ok && event.LogicalVersion < previous.LogicalVersion {
			return nil, a.conflict(ReplayProjectionConflictVersion, event, fmt.Sprintf("page version regressed from %d", previous.LogicalVersion))
		}
		event.AfterImage = cloneReplayImage(event.AfterImage)
		event.TombstoneImage = cloneReplayImage(event.TombstoneImage)
		latest[key] = event
	}
	result := make([]ReplayEvent, 0, len(latest))
	for _, event := range latest {
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool {
		left := replayDependencyOrder(result[i].EntityKind, result[i].Operation)
		right := replayDependencyOrder(result[j].EntityKind, result[j].Operation)
		if left != right {
			return left < right
		}
		return result[i].Sequence < result[j].Sequence
	})
	return result, nil
}

func (a *ReplayProjectionApplier) validateImageIdentity(event ReplayEvent, image ReplayImage) error {
	workspaceID, err := replayRequiredImageField(image, "workspace_id")
	if err != nil {
		return a.conflict(ReplayProjectionConflictWorkspace, event, err.Error())
	}
	if workspaceID != a.workspaceID {
		return a.conflict(ReplayProjectionConflictWorkspace, event, "image workspace="+workspaceID)
	}
	var identity string
	switch event.EntityKind {
	case ReplayEntityProject, ReplayEntityTask, ReplayEntityEvent:
		identity, err = replayRequiredImageField(image, "id")
	case ReplayEntityRule:
		identity, err = replayRequiredImageField(image, "task_id")
	case ReplayEntityOccurrence:
		var taskID, occurrenceDate string
		taskID, err = replayRequiredImageField(image, "task_id")
		if err == nil {
			occurrenceDate, err = replayRequiredImageField(image, "occurrence_date")
		}
		if err == nil {
			encoded, encodeErr := json.Marshal([]string{taskID, occurrenceDate})
			if encodeErr != nil {
				err = encodeErr
			} else {
				identity = string(encoded)
			}
		}
	}
	if err != nil {
		return a.conflict(ReplayProjectionConflictIdentity, event, err.Error())
	}
	if identity != event.SourceID {
		return a.conflict(ReplayProjectionConflictIdentity, event, "image identity="+identity)
	}
	return nil
}

type replayTargetMap struct {
	version int64
	deleted bool
	targets map[string]string
}

func (a *ReplayProjectionApplier) applyEvent(ctx context.Context, tx *sql.Tx, event ReplayEvent) error {
	if err := a.validateSourceLedger(ctx, tx, event); err != nil {
		return err
	}
	mapping, err := a.loadMapping(ctx, tx, event)
	if err != nil {
		var conflict *ReplayProjectionConflictError
		if errors.As(err, &conflict) && conflict.Code == ReplayProjectionConflictMissingMapping {
			if event.Operation == ReplayUpsert {
				return a.insertNewProjection(ctx, tx, event)
			}
			return a.recordUnmaterializedTombstone(ctx, tx, event)
		}
		return err
	}
	if event.LogicalVersion < mapping.version || (mapping.deleted && event.LogicalVersion <= mapping.version) {
		return nil
	}
	if event.LogicalVersion == mapping.version {
		if event.Operation == ReplayDelete {
			return a.conflict(ReplayProjectionConflictVersion, event, "delete shares an active projected version")
		}
		matches, err := a.matchesUpsert(ctx, tx, event, mapping)
		if err != nil {
			return err
		}
		if !matches {
			return a.conflict(ReplayProjectionConflictVersion, event, "same logical version has different projected data")
		}
		return nil
	}

	if event.Operation == ReplayDelete {
		if err := a.deleteTargets(ctx, tx, event, mapping); err != nil {
			return err
		}
	} else {
		if mapping.deleted {
			return a.conflict(ReplayProjectionConflictTarget, event, "newer source recreation needs deterministic remapping")
		}
		if _, err := a.upsertTargets(ctx, tx, event, mapping, false); err != nil {
			return err
		}
	}
	return a.advanceMapping(ctx, tx, event, mapping)
}

func (a *ReplayProjectionApplier) validateSourceLedger(ctx context.Context, tx *sql.Tx, event ReplayEvent) error {
	query := a.bind(`SELECT logical_version,deleted FROM legacy_task_domain_entity_versions
		WHERE workspace_id=? AND entity_kind=? AND entity_id=?`)
	var version int64
	var deleted bool
	err := tx.QueryRowContext(ctx, query, a.workspaceID, event.EntityKind, event.SourceID).Scan(&version, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return a.conflict(ReplayProjectionConflictSourceLedger, event, "durable source-version row is missing")
	}
	if err != nil {
		return fmt.Errorf("load replay source ledger %s/%s: %w", event.EntityKind, event.SourceID, err)
	}
	if version < event.LogicalVersion {
		return a.conflict(ReplayProjectionConflictSourceLedger, event, fmt.Sprintf("durable=%d incoming=%d", version, event.LogicalVersion))
	}
	if version == event.LogicalVersion && deleted != (event.Operation == ReplayDelete) {
		return a.conflict(ReplayProjectionConflictSourceLedger, event, fmt.Sprintf("durable deleted=%t", deleted))
	}
	return nil
}

func (a *ReplayProjectionApplier) loadMapping(ctx context.Context, tx *sql.Tx, event ReplayEvent) (replayTargetMap, error) {
	query := `SELECT target_kind,v2_id,source_logical_version,deleted FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=? ORDER BY target_kind`
	if a.dialect == DialectPostgres {
		query = a.bind(query) + ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query, a.workspaceID, event.EntityKind, event.SourceID)
	if err != nil {
		return replayTargetMap{}, fmt.Errorf("load replay ID map %s/%s: %w", event.EntityKind, event.SourceID, err)
	}
	defer rows.Close()
	mapping := replayTargetMap{targets: make(map[string]string)}
	for rows.Next() {
		var kind, id string
		var version int64
		var deleted bool
		if err := rows.Scan(&kind, &id, &version, &deleted); err != nil {
			return replayTargetMap{}, fmt.Errorf("scan replay ID map: %w", err)
		}
		if len(mapping.targets) == 0 {
			mapping.version, mapping.deleted = version, deleted
		} else if mapping.version != version || mapping.deleted != deleted {
			return replayTargetMap{}, a.conflict(ReplayProjectionConflictMapping, event, "target rows disagree on version/deleted state")
		}
		if _, duplicate := mapping.targets[kind]; duplicate || strings.TrimSpace(id) == "" {
			return replayTargetMap{}, a.conflict(ReplayProjectionConflictMapping, event, "duplicate or empty target kind="+kind)
		}
		mapping.targets[kind] = id
	}
	if err := rows.Err(); err != nil {
		return replayTargetMap{}, fmt.Errorf("iterate replay ID map: %w", err)
	}
	if len(mapping.targets) == 0 {
		return replayTargetMap{}, a.conflict(ReplayProjectionConflictMissingMapping, event, "post-snapshot source needs deterministic mapper")
	}
	if !validReplayTargetShape(event.EntityKind, mapping.targets) {
		return replayTargetMap{}, a.conflict(ReplayProjectionConflictMapping, event, "unexpected target kinds")
	}
	return mapping, nil
}

func validReplayTargetShape(kind ReplayEntityKind, targets map[string]string) bool {
	has := func(key string) bool { return strings.TrimSpace(targets[key]) != "" }
	switch kind {
	case ReplayEntityProject:
		return len(targets) == 1 && has("project")
	case ReplayEntityTask:
		return (len(targets) == 1 && has("task")) || (len(targets) == 3 && has("task") && has("schedule") && has("occurrence"))
	case ReplayEntityRule:
		return len(targets) == 1 && has("schedule")
	case ReplayEntityOccurrence:
		return len(targets) == 1 && has("occurrence")
	case ReplayEntityEvent:
		return len(targets) == 3 && has("task") && has("schedule") && has("occurrence")
	default:
		return false
	}
}

func (a *ReplayProjectionApplier) matchesUpsert(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) (bool, error) {
	return a.upsertTargets(ctx, tx, event, mapping, true)
}

func (a *ReplayProjectionApplier) upsertTargets(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap, verifyOnly bool) (bool, error) {
	switch event.EntityKind {
	case ReplayEntityProject:
		return a.applyProject(ctx, tx, event, mapping, verifyOnly)
	case ReplayEntityTask:
		return a.applyTask(ctx, tx, event, mapping, verifyOnly)
	case ReplayEntityRule:
		return a.applyRule(ctx, tx, event, mapping, verifyOnly)
	case ReplayEntityOccurrence:
		return a.applyOccurrence(ctx, tx, event, mapping, verifyOnly)
	case ReplayEntityEvent:
		return a.applyCalendarEvent(ctx, tx, event, mapping, verifyOnly)
	default:
		return false, a.conflict(ReplayProjectionConflictTarget, event, "unsupported entity kind")
	}
}

func (a *ReplayProjectionApplier) deleteTargets(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) error {
	var table, idColumn, id string
	switch event.EntityKind {
	case ReplayEntityProject:
		table, idColumn, id = "domain_projects_v2", "id", mapping.targets["project"]
	case ReplayEntityTask, ReplayEntityEvent:
		table, idColumn, id = "domain_tasks_v2", "id", mapping.targets["task"]
	case ReplayEntityRule:
		table, idColumn, id = "domain_task_schedules_v2", "task_id", mapping.targets["schedule"]
	case ReplayEntityOccurrence:
		table, idColumn, id = "domain_task_occurrences_v2", "id", mapping.targets["occurrence"]
	}
	result, err := tx.ExecContext(ctx, a.bind(`DELETE FROM `+table+` WHERE workspace_id=? AND `+idColumn+`=?`), a.workspaceID, id)
	if err != nil {
		return fmt.Errorf("delete replay target %s/%s: %w", table, id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read replay target delete result: %w", err)
	}
	if rows != 1 {
		return a.conflict(ReplayProjectionConflictTarget, event, "mapped target is absent")
	}
	return nil
}

func (a *ReplayProjectionApplier) advanceMapping(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) error {
	deleted := event.Operation == ReplayDelete
	query := a.bind(`UPDATE task_domain_legacy_id_map
		SET source_logical_version=?,deleted=?,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=? AND source_logical_version=? AND deleted=?`)
	result, err := tx.ExecContext(ctx, query, event.LogicalVersion, deleted, a.workspaceID, event.EntityKind, event.SourceID, mapping.version, mapping.deleted)
	if err != nil {
		return fmt.Errorf("advance replay ID map %s/%s: %w", event.EntityKind, event.SourceID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read replay ID map update result: %w", err)
	}
	if rows != int64(len(mapping.targets)) {
		return a.conflict(ReplayProjectionConflictVersion, event, fmt.Sprintf("mapping CAS changed %d of %d rows", rows, len(mapping.targets)))
	}
	return nil
}

func (a *ReplayProjectionApplier) bind(query string) string {
	if a.dialect != DialectPostgres {
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

func (a *ReplayProjectionApplier) conflict(code ReplayProjectionConflictCode, event ReplayEvent, detail string) error {
	return &ReplayProjectionConflictError{
		Code: code, WorkspaceID: a.workspaceID,
		Reference: fmt.Sprintf("%s/%s@%d", event.EntityKind, event.SourceID, event.Sequence), Detail: detail,
	}
}

func replayRequiredImageField(image ReplayImage, name string) (string, error) {
	value, ok := image[name]
	value = strings.TrimSpace(value)
	if !ok || value == "" || value == "null" {
		return "", fmt.Errorf("required image field %q is missing", name)
	}
	return value, nil
}

func replayOptionalImageField(image ReplayImage, name string) string {
	value := strings.TrimSpace(image[name])
	if value == "null" {
		return ""
	}
	return value
}

func replayIntField(image ReplayImage, name string, min, max int64) (int64, error) {
	value, err := replayRequiredImageField(image, name)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < min || parsed > max {
		return 0, fmt.Errorf("invalid integer field %q=%q", name, value)
	}
	return parsed, nil
}

func replayFloatField(image ReplayImage, name string) (float64, error) {
	value, err := replayRequiredImageField(image, name)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("invalid numeric field %q=%q", name, value)
	}
	return parsed, nil
}

func replayBoolField(image ReplayImage, name string) (bool, error) {
	value, err := replayRequiredImageField(image, name)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(value) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean field %q=%q", name, value)
	}
}

func replayTimeField(image ReplayImage, name string, required bool) (*time.Time, error) {
	value := replayOptionalImageField(image, name)
	if value == "" {
		if required {
			return nil, fmt.Errorf("required time field %q is missing", name)
		}
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid time field %q=%q", name, value)
}

func replayTimeFieldAliases(image ReplayImage, required bool, names ...string) (*time.Time, error) {
	for _, name := range names {
		if value, ok := image[name]; ok && replayOptionalImageField(image, name) != "" && strings.TrimSpace(value) != "" {
			return replayTimeField(image, name, true)
		}
	}
	if required {
		return nil, fmt.Errorf("missing %s", strings.Join(names, "/"))
	}
	return nil, nil
}

func replayDateField(image ReplayImage, name string, required bool) (string, error) {
	value := replayOptionalImageField(image, name)
	if value == "" {
		if required {
			return "", fmt.Errorf("required date field %q is missing", name)
		}
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", fmt.Errorf("invalid date field %q=%q", name, value)
	}
	return value, nil
}

func replayIntArrayField(image ReplayImage, name string) ([]int, error) {
	value := replayOptionalImageField(image, name)
	if value == "" {
		return nil, nil
	}
	var result []int
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("invalid integer array field %q: %w", name, err)
	}
	return normalizedInts(result), nil
}

func replayNullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func replayDBTime(dialect Dialect, value *time.Time) any {
	if value == nil {
		return nil
	}
	if dialect == DialectPostgres {
		return value.UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func replayScanString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(typed)
	}
}

func replaySameTime(value any, expected *time.Time) bool {
	if expected == nil {
		return value == nil
	}
	actual := replayScanString(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05+00:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, actual); err == nil {
			return parsed.Equal(*expected)
		}
	}
	return false
}

func replaySameOptional(value any, expected string) bool {
	if expected == "" {
		return value == nil || replayScanString(value) == ""
	}
	return replayScanString(value) == expected
}

func replaySameDate(value any, expected string) bool {
	if expected == "" {
		return value == nil || replayScanString(value) == ""
	}
	if instant, ok := value.(time.Time); ok {
		return instant.Format("2006-01-02") == expected
	}
	actual := replayScanString(value)
	if len(actual) >= len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", actual[:10]); err == nil {
			return actual[:10] == expected
		}
	}
	return actual == expected
}

func replayStatus(value string) (taskdomain.ExecutionStatus, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "todo", "open":
		return taskdomain.ExecutionStatusOpen, nil
	case "doing", "in_progress", "active":
		return taskdomain.ExecutionStatusActive, nil
	case "blocked":
		return taskdomain.ExecutionStatusBlocked, nil
	case "done", "completed":
		return taskdomain.ExecutionStatusDone, nil
	case "skipped":
		return taskdomain.ExecutionStatusSkipped, nil
	case "cancelled", "canceled":
		return taskdomain.ExecutionStatusCancelled, nil
	default:
		return "", fmt.Errorf("unsupported legacy status %q", value)
	}
}

func replayLifecycle(status taskdomain.ExecutionStatus, recurring bool) taskdomain.TaskLifecycleStatus {
	if recurring {
		return taskdomain.TaskLifecycleActive
	}
	switch status {
	case taskdomain.ExecutionStatusDone:
		return taskdomain.TaskLifecycleCompleted
	case taskdomain.ExecutionStatusCancelled:
		return taskdomain.TaskLifecycleCancelled
	default:
		return taskdomain.TaskLifecycleActive
	}
}

func replayProjectCapabilities(projectType string) (kind, horizon string, err error) {
	switch LegacyProjectType(strings.ToLower(projectType)) {
	case LegacyProjectPersonal:
		return "standard", "short", nil
	case LegacyProjectRegular:
		return "standard", "long", nil
	case LegacyProjectLearning:
		return "learning", "long", nil
	default:
		return "", "", fmt.Errorf("unsupported legacy project type %q", projectType)
	}
}

func replayFloatEqual(left, right float64) bool {
	return math.Abs(left-right) < 0.0000001
}

func (a *ReplayProjectionApplier) applyProject(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	verifyOnly bool,
) (bool, error) {
	name, err := replayRequiredImageField(event.AfterImage, "name")
	if err != nil {
		return false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	projectType, err := replayRequiredImageField(event.AfterImage, "type")
	if err != nil {
		return false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	kind, horizon, err := replayProjectCapabilities(projectType)
	if err != nil {
		return false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}

	var currentName, currentKind, currentHorizon string
	var systemRole sql.NullString
	err = tx.QueryRowContext(ctx, a.bind(`SELECT name,kind,horizon,system_role FROM domain_projects_v2
		WHERE workspace_id=? AND id=?`), a.workspaceID, mapping.targets["project"]).
		Scan(&currentName, &currentKind, &currentHorizon, &systemRole)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped project is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay project target: %w", err)
	}
	// A preflight-selected system role is immutable. Its capabilities belong
	// to the system target, not to subsequent edits of the legacy type field.
	if systemRole.Valid {
		kind, horizon = currentKind, currentHorizon
	}
	matches := currentName == name && currentKind == kind && currentHorizon == horizon
	if verifyOnly || matches {
		return matches, nil
	}
	result, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_projects_v2
		SET name=?,kind=?,horizon=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=?`), name, kind, horizon, a.workspaceID, mapping.targets["project"])
	if err != nil {
		return false, fmt.Errorf("update replay project target: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped project changed")
	}
	return true, nil
}

func (a *ReplayProjectionApplier) resolveProjectTarget(ctx context.Context, tx *sql.Tx, event ReplayEvent, legacyProjectID string) (string, error) {
	if legacyProjectID == "" {
		var target string
		err := tx.QueryRowContext(ctx, a.bind(`SELECT id FROM domain_projects_v2
			WHERE workspace_id=? AND system_role='personal'`), a.workspaceID).Scan(&target)
		if errors.Is(err, sql.ErrNoRows) {
			return "", a.conflict(ReplayProjectionConflictDependency, event, "personal system project is absent")
		}
		if err != nil {
			return "", fmt.Errorf("resolve personal project: %w", err)
		}
		return target, nil
	}
	return a.resolveMappedTarget(ctx, tx, event, ReplayEntityProject, legacyProjectID, "project")
}

func (a *ReplayProjectionApplier) resolveMappedTarget(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	entityKind ReplayEntityKind,
	legacyID string,
	targetKind string,
) (string, error) {
	var target string
	var deleted bool
	err := tx.QueryRowContext(ctx, a.bind(`SELECT v2_id,deleted FROM task_domain_legacy_id_map
		WHERE workspace_id=? AND entity_kind=? AND legacy_id=? AND target_kind=?`),
		a.workspaceID, entityKind, legacyID, targetKind).Scan(&target, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return "", a.conflict(ReplayProjectionConflictDependency, event,
			fmt.Sprintf("missing %s mapping %s/%s", targetKind, entityKind, legacyID))
	}
	if err != nil {
		return "", fmt.Errorf("resolve replay dependency %s/%s: %w", entityKind, legacyID, err)
	}
	if deleted {
		return "", a.conflict(ReplayProjectionConflictDependency, event,
			fmt.Sprintf("dependency %s/%s is deleted", entityKind, legacyID))
	}
	return target, nil
}

type replayTaskTarget struct {
	projectID     string
	roadmapNodeID string
	noteID        string
	title         string
	content       string
	lifecycle     taskdomain.TaskLifecycleStatus
	priority      int64
	sortOrder     float64
	status        taskdomain.ExecutionStatus
	planned       string
	due           *time.Time
	completed     *time.Time
	timezone      string
	blocked       string
	next          string
}

func (a *ReplayProjectionApplier) desiredTask(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) (replayTaskTarget, bool, error) {
	image := event.AfterImage
	projectID, err := a.resolveProjectTarget(ctx, tx, event, replayOptionalImageField(image, "project_id"))
	if err != nil {
		return replayTaskTarget{}, false, err
	}
	roadmapNodeID := ""
	if legacyRoadmapNodeID := replayOptionalImageField(image, "roadmap_node_id"); legacyRoadmapNodeID != "" {
		roadmapNodeID, err = a.resolveMappedTarget(ctx, tx, event, ReplayEntityRoadmapNode, legacyRoadmapNodeID, "roadmap_node")
		if err != nil {
			return replayTaskTarget{}, false, err
		}
		var nodeProjectID string
		err = tx.QueryRowContext(ctx, a.bind(`SELECT project_id FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=?`), a.workspaceID, roadmapNodeID).Scan(&nodeProjectID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && nodeProjectID != projectID) {
			return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictDependency, event, "roadmap node is absent or belongs to another project")
		}
		if err != nil {
			return replayTaskTarget{}, false, fmt.Errorf("resolve replay task roadmap node: %w", err)
		}
	}
	title, err := replayRequiredImageField(image, "title")
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	content := replayOptionalImageField(image, "content")
	priority, err := replayIntField(image, "priority", 0, 3)
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	sortOrder, err := replayFloatField(image, "sort_order")
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	status, err := replayStatus(replayOptionalImageField(image, "status"))
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	done, err := replayBoolField(image, "done")
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if done {
		status = taskdomain.ExecutionStatusDone
	}
	executionType := replayOptionalImageField(image, "execution_type")
	if executionType == "" {
		executionType = string(LegacyExecutionSingle)
	}
	recurring := executionType == string(LegacyExecutionRecurring)
	if !recurring && executionType != string(LegacyExecutionSingle) {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, "unsupported execution_type="+executionType)
	}
	if recurring != (mapping.targets["schedule"] == "") {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictMapping, event, "execution type disagrees with snapshot mapping shape")
	}
	planned, err := replayDateField(image, "planned_date", false)
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	due, err := replayTimeFieldAliases(image, false, "due_at", "due")
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	completed, err := replayTimeField(image, "completed_at", false)
	if err != nil {
		return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if status == taskdomain.ExecutionStatusDone && completed == nil {
		completed, err = replayTimeField(image, "updated_at", true)
		if err != nil {
			return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
		}
	}
	if status != taskdomain.ExecutionStatusDone {
		completed = nil
	}
	target := replayTaskTarget{
		projectID: projectID, roadmapNodeID: roadmapNodeID, noteID: replayOptionalImageField(image, "note_id"), title: title, content: content,
		lifecycle: replayLifecycle(status, recurring), priority: priority, sortOrder: sortOrder,
		status: status, planned: planned, due: due, completed: completed,
	}
	if !recurring {
		err = tx.QueryRowContext(ctx, a.bind(`SELECT timezone FROM domain_task_schedule_versions_v2
			WHERE workspace_id=? AND task_id=? AND schedule_revision=1`), a.workspaceID, mapping.targets["schedule"]).Scan(&target.timezone)
		if errors.Is(err, sql.ErrNoRows) {
			return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, "mapped schedule version is absent")
		}
		if err != nil {
			return replayTaskTarget{}, false, fmt.Errorf("load task schedule timezone: %w", err)
		}
		if status == taskdomain.ExecutionStatusBlocked {
			err = tx.QueryRowContext(ctx, a.bind(`SELECT blocked_reason,next_action FROM domain_task_occurrences_v2
				WHERE workspace_id=? AND id=?`), a.workspaceID, mapping.targets["occurrence"]).Scan(&target.blocked, &target.next)
			if err != nil || strings.TrimSpace(target.blocked) == "" || strings.TrimSpace(target.next) == "" {
				return replayTaskTarget{}, false, a.conflict(ReplayProjectionConflictTarget, event, "blocked task lacks durable reason/next action")
			}
		}
	}
	return target, recurring, nil
}

func (a *ReplayProjectionApplier) applyTask(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	verifyOnly bool,
) (bool, error) {
	desired, recurring, err := a.desiredTask(ctx, tx, event, mapping)
	if err != nil {
		return false, err
	}
	matches, err := a.taskTargetMatches(ctx, tx, event, mapping, desired, recurring)
	if err != nil || verifyOnly || matches {
		return matches, err
	}
	result, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_tasks_v2
		SET project_id=?,roadmap_node_id=?,note_id=?,title=?,description=?,lifecycle_status=?,priority=?,sort_order=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=?`), desired.projectID, replayNullable(desired.roadmapNodeID), replayNullable(desired.noteID), desired.title, desired.content,
		desired.lifecycle, desired.priority, desired.sortOrder, a.workspaceID, mapping.targets["task"])
	if err != nil {
		return false, fmt.Errorf("update replay task target: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped task changed")
	}
	if recurring {
		return true, nil
	}
	timing := string(taskdomain.TimingUnscheduled)
	if desired.planned != "" {
		timing = string(taskdomain.TimingDate)
	}
	if _, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_task_schedule_versions_v2
		SET effective_from=NULL,effective_to=NULL,recurrence_type='none',timing_type=?,timezone=?,starts_on=?,ends_on=NULL,
			recurrence_rule=`+a.jsonValuePlaceholder()+`,local_start_time=NULL,duration_minutes=NULL
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`),
		timing, desired.timezone, replayNullable(desired.planned), `{}`, a.workspaceID, mapping.targets["schedule"]); err != nil {
		return false, fmt.Errorf("update replay single-task schedule version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_task_schedules_v2
		SET revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND task_id=?`),
		a.workspaceID, mapping.targets["schedule"]); err != nil {
		return false, fmt.Errorf("update replay single-task schedule: %w", err)
	}
	blocked, next := any(nil), any(nil)
	if desired.status == taskdomain.ExecutionStatusBlocked {
		blocked, next = desired.blocked, desired.next
	}
	result, err = tx.ExecContext(ctx, a.bind(`UPDATE domain_task_occurrences_v2
		SET planned_date=?,planned_start_at=NULL,planned_end_at=NULL,due_at=?,execution_status=?,completed_at=?,
			all_day_end_date=NULL,blocked_reason=?,next_action=?,generated_schedule_revision=1,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=? AND task_id=?`), replayNullable(desired.planned), replayDBTime(a.dialect, desired.due), desired.status,
		replayDBTime(a.dialect, desired.completed), blocked, next, a.workspaceID, mapping.targets["occurrence"], mapping.targets["task"])
	if err != nil {
		return false, fmt.Errorf("update replay single-task occurrence: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped single-task occurrence changed")
	}
	return true, nil
}

func (a *ReplayProjectionApplier) taskTargetMatches(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	desired replayTaskTarget,
	recurring bool,
) (bool, error) {
	var projectID, title, description, lifecycle string
	var roadmapNodeID, noteID sql.NullString
	var priority int64
	var sortOrder float64
	err := tx.QueryRowContext(ctx, a.bind(`SELECT project_id,roadmap_node_id,note_id,title,description,lifecycle_status,priority,sort_order
		FROM domain_tasks_v2 WHERE workspace_id=? AND id=?`), a.workspaceID, mapping.targets["task"]).
		Scan(&projectID, &roadmapNodeID, &noteID, &title, &description, &lifecycle, &priority, &sortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped task is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay task target: %w", err)
	}
	if projectID != desired.projectID || roadmapNodeID.String != desired.roadmapNodeID || noteID.String != desired.noteID || title != desired.title || description != desired.content ||
		lifecycle != string(desired.lifecycle) || priority != desired.priority || !replayFloatEqual(sortOrder, desired.sortOrder) {
		return false, nil
	}
	if recurring {
		return true, nil
	}
	var recurrence, timing, timezone string
	var startsOn, endsOn any
	if err := tx.QueryRowContext(ctx, a.bind(`SELECT recurrence_type,timing_type,timezone,starts_on,ends_on
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? AND schedule_revision=1`),
		a.workspaceID, mapping.targets["schedule"]).Scan(&recurrence, &timing, &timezone, &startsOn, &endsOn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped schedule version is absent")
		}
		return false, fmt.Errorf("load replay task schedule target: %w", err)
	}
	wantTiming := string(taskdomain.TimingUnscheduled)
	if desired.planned != "" {
		wantTiming = string(taskdomain.TimingDate)
	}
	if recurrence != "none" || timing != wantTiming || timezone != desired.timezone || !replaySameDate(startsOn, desired.planned) || endsOn != nil {
		return false, nil
	}
	var planned, due, completed any
	var status string
	var blocked, next sql.NullString
	err = tx.QueryRowContext(ctx, a.bind(`SELECT planned_date,due_at,execution_status,completed_at,blocked_reason,next_action
		FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=? AND task_id=?`),
		a.workspaceID, mapping.targets["occurrence"], mapping.targets["task"]).
		Scan(&planned, &due, &status, &completed, &blocked, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped occurrence is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay task occurrence target: %w", err)
	}
	return replaySameDate(planned, desired.planned) && replaySameTime(due, desired.due) && status == string(desired.status) &&
		replaySameTime(completed, desired.completed) && blocked.String == desired.blocked && next.String == desired.next, nil
}

func (a *ReplayProjectionApplier) jsonValuePlaceholder() string {
	if a.dialect == DialectPostgres {
		return "CAST(? AS JSONB)"
	}
	return "?"
}

type replayRuleTarget struct {
	recurrence taskdomain.RecurrenceType
	timing     taskdomain.TimingType
	timezone   string
	startsOn   string
	endsOn     string
	rule       string
	localStart string
	duration   int
	effective  string
}

func (a *ReplayProjectionApplier) desiredRule(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) (replayRuleTarget, error) {
	var current replayRuleTarget
	var startsOn, endsOn, localStart sql.NullString
	var duration sql.NullInt64
	err := tx.QueryRowContext(ctx, a.bind(`SELECT recurrence_type,timing_type,timezone,starts_on,ends_on,local_start_time,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? AND schedule_revision=1`),
		a.workspaceID, mapping.targets["schedule"]).Scan(&current.recurrence, &current.timing, &current.timezone, &startsOn, &endsOn, &localStart, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "mapped rule schedule is absent")
	}
	if err != nil {
		return replayRuleTarget{}, fmt.Errorf("load replay rule schedule: %w", err)
	}
	enabled, err := replayBoolField(event.AfterImage, "enabled")
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if !enabled {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "disabled recurrence rule requires an explicit task command")
	}
	frequency, err := replayRequiredImageField(event.AfterImage, "frequency")
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	switch taskdomain.RecurrenceType(frequency) {
	case taskdomain.RecurrenceDaily, taskdomain.RecurrenceWeekly, taskdomain.RecurrenceMonthly:
		current.recurrence = taskdomain.RecurrenceType(frequency)
	default:
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "unsupported frequency="+frequency)
	}
	interval, err := replayIntField(event.AfterImage, "interval", 1, math.MaxInt32)
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	weekdays, err := replayIntArrayField(event.AfterImage, "weekdays")
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	monthDays, err := replayIntArrayField(event.AfterImage, "month_days")
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	current.startsOn, err = replayDateField(event.AfterImage, "start_date", true)
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	current.endsOn, err = replayDateField(event.AfterImage, "end_date", false)
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if current.endsOn != "" && current.endsOn < current.startsOn {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "end_date precedes start_date")
	}
	timezone := replayOptionalImageField(event.AfterImage, "timezone")
	if timezone != "" {
		if _, err := time.LoadLocation(timezone); err != nil {
			return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "invalid timezone="+timezone)
		}
		current.timezone = timezone
	}
	if current.timing == taskdomain.TimingUnscheduled {
		current.timing = taskdomain.TimingDate
	}
	current.localStart = localStart.String
	current.duration = int(duration.Int64)
	if current.timing != taskdomain.TimingTimeBlock {
		current.localStart, current.duration = "", 0
	}
	current.effective = current.startsOn
	projected := V2ScheduleProjection{RecurrenceType: current.recurrence, Interval: int(interval), Weekdays: weekdays, MonthDays: monthDays}
	current.rule, err = projectedRecurrenceRule(projected)
	if err != nil {
		return replayRuleTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	return current, nil
}

func (a *ReplayProjectionApplier) applyRule(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	verifyOnly bool,
) (bool, error) {
	desired, err := a.desiredRule(ctx, tx, event, mapping)
	if err != nil {
		return false, err
	}
	var recurrence, timing, timezone string
	var effective, startsOn, endsOn, rule, localStart, duration any
	err = tx.QueryRowContext(ctx, a.bind(`SELECT recurrence_type,timing_type,timezone,effective_from,starts_on,ends_on,
		recurrence_rule,local_start_time,duration_minutes FROM domain_task_schedule_versions_v2
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`), a.workspaceID, mapping.targets["schedule"]).
		Scan(&recurrence, &timing, &timezone, &effective, &startsOn, &endsOn, &rule, &localStart, &duration)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped rule schedule is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay rule target: %w", err)
	}
	durationValue := 0
	if duration != nil {
		parsed, parseErr := strconv.Atoi(replayScanString(duration))
		if parseErr != nil {
			return false, fmt.Errorf("decode replay schedule duration: %w", parseErr)
		}
		durationValue = parsed
	}
	matches := recurrence == string(desired.recurrence) && timing == string(desired.timing) && timezone == desired.timezone &&
		replaySameDate(effective, desired.effective) && replaySameDate(startsOn, desired.startsOn) &&
		replaySameDate(endsOn, desired.endsOn) && replayScanString(rule) == desired.rule &&
		replaySameOptional(localStart, desired.localStart) && durationValue == desired.duration
	if verifyOnly || matches {
		return matches, nil
	}
	query := `UPDATE domain_task_schedule_versions_v2
		SET effective_from=?,effective_to=NULL,recurrence_type=?,timing_type=?,timezone=?,starts_on=?,ends_on=?,
			recurrence_rule=` + a.jsonValuePlaceholder() + `,local_start_time=?,duration_minutes=?
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`
	result, err := tx.ExecContext(ctx, a.bind(query), replayNullable(desired.effective), desired.recurrence, desired.timing,
		desired.timezone, replayNullable(desired.startsOn), replayNullable(desired.endsOn), desired.rule,
		replayNullable(desired.localStart), replayNullablePositiveInt(desired.duration), a.workspaceID, mapping.targets["schedule"])
	if err != nil {
		return false, fmt.Errorf("update replay rule schedule version: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped rule schedule changed")
	}
	if _, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_task_schedules_v2
		SET revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND task_id=?`),
		a.workspaceID, mapping.targets["schedule"]); err != nil {
		return false, fmt.Errorf("update replay rule schedule header: %w", err)
	}
	return true, nil
}

func replayNullablePositiveInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

type replayOccurrenceTarget struct {
	taskID    string
	planned   string
	status    taskdomain.ExecutionStatus
	completed *time.Time
	notes     string
	blocked   string
	next      string
}

func (a *ReplayProjectionApplier) desiredOccurrence(ctx context.Context, tx *sql.Tx, event ReplayEvent, mapping replayTargetMap) (replayOccurrenceTarget, error) {
	image := event.AfterImage
	legacyTaskID, err := replayRequiredImageField(image, "task_id")
	if err != nil {
		return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	taskID, err := a.resolveMappedTarget(ctx, tx, event, ReplayEntityTask, legacyTaskID, "task")
	if err != nil {
		return replayOccurrenceTarget{}, err
	}
	planned, err := replayDateField(image, "occurrence_date", true)
	if err != nil {
		return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	status, err := replayStatus(replayOptionalImageField(image, "status"))
	if err != nil {
		return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	completed, err := replayTimeField(image, "completed_at", false)
	if err != nil {
		return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if status == taskdomain.ExecutionStatusDone && completed == nil {
		completed, err = replayTimeField(image, "updated_at", true)
		if err != nil {
			return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
		}
	}
	if status != taskdomain.ExecutionStatusDone {
		completed = nil
	}
	target := replayOccurrenceTarget{taskID: taskID, planned: planned, status: status, completed: completed, notes: replayOptionalImageField(image, "note")}
	if status == taskdomain.ExecutionStatusBlocked {
		err = tx.QueryRowContext(ctx, a.bind(`SELECT blocked_reason,next_action FROM domain_task_occurrences_v2
			WHERE workspace_id=? AND id=?`), a.workspaceID, mapping.targets["occurrence"]).Scan(&target.blocked, &target.next)
		if err != nil || strings.TrimSpace(target.blocked) == "" || strings.TrimSpace(target.next) == "" {
			return replayOccurrenceTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "blocked occurrence lacks durable reason/next action")
		}
	}
	return target, nil
}

func (a *ReplayProjectionApplier) applyOccurrence(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	verifyOnly bool,
) (bool, error) {
	desired, err := a.desiredOccurrence(ctx, tx, event, mapping)
	if err != nil {
		return false, err
	}
	var taskID, status string
	var planned, completed, notes any
	var blocked, next sql.NullString
	err = tx.QueryRowContext(ctx, a.bind(`SELECT task_id,planned_date,execution_status,completed_at,calendar_notes,blocked_reason,next_action
		FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=?`), a.workspaceID, mapping.targets["occurrence"]).
		Scan(&taskID, &planned, &status, &completed, &notes, &blocked, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped occurrence is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay occurrence target: %w", err)
	}
	matches := taskID == desired.taskID && replaySameDate(planned, desired.planned) && status == string(desired.status) &&
		replaySameTime(completed, desired.completed) && replaySameOptional(notes, desired.notes) && blocked.String == desired.blocked && next.String == desired.next
	if verifyOnly || matches {
		return matches, nil
	}
	blockedValue, nextValue := any(nil), any(nil)
	if desired.status == taskdomain.ExecutionStatusBlocked {
		blockedValue, nextValue = desired.blocked, desired.next
	}
	result, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_task_occurrences_v2
		SET planned_date=?,execution_status=?,completed_at=?,calendar_notes=?,blocked_reason=?,next_action=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=? AND task_id=?`), desired.planned, desired.status, replayDBTime(a.dialect, desired.completed),
		replayNullable(desired.notes), blockedValue, nextValue, a.workspaceID, mapping.targets["occurrence"], desired.taskID)
	if err != nil {
		return false, fmt.Errorf("update replay occurrence target: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped occurrence changed")
	}
	return true, nil
}

type replayCalendarTarget struct {
	projectID       string
	taskID          string
	occurrenceID    string
	title           string
	description     string
	lifecycle       string
	priority        int64
	sortOrder       float64
	taskNoteID      string
	schedule        V2ScheduleProjection
	occurrence      V2OccurrenceProjection
	executionStatus string
	completedAt     any
	blocked         sql.NullString
	next            sql.NullString
}

func (a *ReplayProjectionApplier) desiredCalendarEvent(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
) (replayCalendarTarget, error) {
	projectID, err := a.resolveProjectTarget(ctx, tx, event, replayOptionalImageField(event.AfterImage, "project_id"))
	if err != nil {
		return replayCalendarTarget{}, err
	}
	title, err := replayRequiredImageField(event.AfterImage, "title")
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	start, err := replayTimeFieldAliases(event.AfterImage, true, "start_time", "start_at")
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	end, err := replayTimeFieldAliases(event.AfterImage, true, "end_time", "end_at")
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	allDay, err := replayBoolField(event.AfterImage, "is_all_day")
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	var timezone string
	err = tx.QueryRowContext(ctx, a.bind(`SELECT timezone FROM domain_task_schedule_versions_v2
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`), a.workspaceID, mapping.targets["schedule"]).Scan(&timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "mapped event schedule is absent")
	}
	if err != nil {
		return replayCalendarTarget{}, fmt.Errorf("load replay event timezone: %w", err)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "invalid mapped timezone="+timezone)
	}
	task, schedule, occurrence, err := mapLegacyEvent(LegacyEventRow{
		ID: event.SourceID, ProjectID: replayOptionalImageField(event.AfterImage, "project_id"), Title: title,
		StartAt: *start, EndAt: *end, AllDay: allDay, Location: replayOptionalImageField(event.AfterImage, "location"),
		Kind: replayOptionalImageField(event.AfterImage, "kind"), Notes: replayOptionalImageField(event.AfterImage, "notes"),
		NoteID: replayOptionalImageField(event.AfterImage, "note_id"),
	}, projectID, timezone, location)
	if err != nil {
		return replayCalendarTarget{}, err
	}
	task.ID = mapping.targets["task"]
	schedule.TaskID = mapping.targets["schedule"]
	occurrence.ID = mapping.targets["occurrence"]
	occurrence.TaskID = mapping.targets["task"]
	schedule, err = normalizeProjectedSchedule(schedule, []V2OccurrenceProjection{occurrence})
	if err != nil {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	target := replayCalendarTarget{
		projectID: projectID, taskID: task.ID, occurrenceID: occurrence.ID, title: title,
		schedule: schedule, occurrence: occurrence,
	}
	var noteID sql.NullString
	err = tx.QueryRowContext(ctx, a.bind(`SELECT description,lifecycle_status,priority,sort_order,note_id FROM domain_tasks_v2
		WHERE workspace_id=? AND id=?`), a.workspaceID, target.taskID).
		Scan(&target.description, &target.lifecycle, &target.priority, &target.sortOrder, &noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "mapped event task is absent")
	}
	if err != nil {
		return replayCalendarTarget{}, fmt.Errorf("load replay event task: %w", err)
	}
	target.taskNoteID = noteID.String
	err = tx.QueryRowContext(ctx, a.bind(`SELECT execution_status,completed_at,blocked_reason,next_action
		FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=? AND task_id=?`),
		a.workspaceID, target.occurrenceID, target.taskID).
		Scan(&target.executionStatus, &target.completedAt, &target.blocked, &target.next)
	if errors.Is(err, sql.ErrNoRows) {
		return replayCalendarTarget{}, a.conflict(ReplayProjectionConflictTarget, event, "mapped event occurrence is absent")
	}
	if err != nil {
		return replayCalendarTarget{}, fmt.Errorf("load replay event occurrence: %w", err)
	}
	return target, nil
}

func (a *ReplayProjectionApplier) applyCalendarEvent(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	mapping replayTargetMap,
	verifyOnly bool,
) (bool, error) {
	desired, err := a.desiredCalendarEvent(ctx, tx, event, mapping)
	if err != nil {
		return false, err
	}
	matches, err := a.calendarEventMatches(ctx, tx, event, desired)
	if err != nil || verifyOnly || matches {
		return matches, err
	}
	result, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_tasks_v2
		SET project_id=?,title=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=?`), desired.projectID, desired.title, a.workspaceID, desired.taskID)
	if err != nil {
		return false, fmt.Errorf("update replay event task: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped event task changed")
	}
	rule, err := projectedRecurrenceRule(desired.schedule)
	if err != nil {
		return false, a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	query := `UPDATE domain_task_schedule_versions_v2
		SET effective_from=NULL,effective_to=NULL,recurrence_type='none',timing_type=?,timezone=?,starts_on=?,ends_on=NULL,
			recurrence_rule=` + a.jsonValuePlaceholder() + `,local_start_time=?,duration_minutes=?
		WHERE workspace_id=? AND task_id=? AND schedule_revision=1`
	result, err = tx.ExecContext(ctx, a.bind(query), desired.schedule.TimingType, desired.schedule.Timezone,
		replayNullable(desired.schedule.StartsOn), rule, replayNullable(desired.schedule.LocalStartTime),
		replayNullablePositiveInt(desired.schedule.DurationMinutes), a.workspaceID, desired.taskID)
	if err != nil {
		return false, fmt.Errorf("update replay event schedule version: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped event schedule changed")
	}
	if _, err := tx.ExecContext(ctx, a.bind(`UPDATE domain_task_schedules_v2
		SET revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND task_id=?`), a.workspaceID, desired.taskID); err != nil {
		return false, fmt.Errorf("update replay event schedule header: %w", err)
	}
	result, err = tx.ExecContext(ctx, a.bind(`UPDATE domain_task_occurrences_v2
		SET planned_date=?,planned_start_at=?,planned_end_at=?,location=?,calendar_kind=?,calendar_notes=?,note_id=?,all_day_end_date=?,
			generated_schedule_revision=1,revision=revision+1,updated_at=CURRENT_TIMESTAMP
		WHERE workspace_id=? AND id=? AND task_id=?`), replayNullable(desired.occurrence.PlannedDate),
		replayDBTime(a.dialect, desired.occurrence.PlannedStartAt), replayDBTime(a.dialect, desired.occurrence.PlannedEndAt),
		replayNullable(desired.occurrence.Location), replayNullable(desired.occurrence.CalendarKind), replayNullable(desired.occurrence.CalendarNotes),
		replayNullable(desired.occurrence.OccurrenceNoteID), replayNullable(desired.occurrence.AllDayEndDate),
		a.workspaceID, desired.occurrenceID, desired.taskID)
	if err != nil {
		return false, fmt.Errorf("update replay event occurrence: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return false, rowsErr
		}
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped event occurrence changed")
	}
	return true, nil
}

func (a *ReplayProjectionApplier) calendarEventMatches(
	ctx context.Context,
	tx *sql.Tx,
	event ReplayEvent,
	desired replayCalendarTarget,
) (bool, error) {
	var projectID, title string
	err := tx.QueryRowContext(ctx, a.bind(`SELECT project_id,title FROM domain_tasks_v2 WHERE workspace_id=? AND id=?`),
		a.workspaceID, desired.taskID).Scan(&projectID, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return false, a.conflict(ReplayProjectionConflictTarget, event, "mapped event task is absent")
	}
	if err != nil {
		return false, fmt.Errorf("load replay event task match: %w", err)
	}
	if projectID != desired.projectID || title != desired.title {
		return false, nil
	}
	var recurrence, timing, timezone string
	var startsOn, rule, localStart, duration any
	err = tx.QueryRowContext(ctx, a.bind(`SELECT recurrence_type,timing_type,timezone,starts_on,recurrence_rule,local_start_time,duration_minutes
		FROM domain_task_schedule_versions_v2 WHERE workspace_id=? AND task_id=? AND schedule_revision=1`),
		a.workspaceID, desired.taskID).Scan(&recurrence, &timing, &timezone, &startsOn, &rule, &localStart, &duration)
	if err != nil {
		return false, fmt.Errorf("load replay event schedule match: %w", err)
	}
	wantRule, _ := projectedRecurrenceRule(desired.schedule)
	wantDuration := 0
	if desired.schedule.DurationMinutes > 0 {
		wantDuration = desired.schedule.DurationMinutes
	}
	actualDuration := 0
	if duration != nil {
		actualDuration, _ = strconv.Atoi(replayScanString(duration))
	}
	if recurrence != "none" || timing != string(desired.schedule.TimingType) || timezone != desired.schedule.Timezone ||
		!replaySameDate(startsOn, desired.schedule.StartsOn) || replayScanString(rule) != wantRule ||
		!replaySameOptional(localStart, desired.schedule.LocalStartTime) || actualDuration != wantDuration {
		return false, nil
	}
	var plannedDate, startAt, endAt, location, kind, notes, noteID, allDayEnd any
	err = tx.QueryRowContext(ctx, a.bind(`SELECT planned_date,planned_start_at,planned_end_at,location,calendar_kind,calendar_notes,note_id,all_day_end_date
		FROM domain_task_occurrences_v2 WHERE workspace_id=? AND id=? AND task_id=?`),
		a.workspaceID, desired.occurrenceID, desired.taskID).
		Scan(&plannedDate, &startAt, &endAt, &location, &kind, &notes, &noteID, &allDayEnd)
	if err != nil {
		return false, fmt.Errorf("load replay event occurrence match: %w", err)
	}
	return replaySameDate(plannedDate, desired.occurrence.PlannedDate) &&
		replaySameTime(startAt, desired.occurrence.PlannedStartAt) && replaySameTime(endAt, desired.occurrence.PlannedEndAt) &&
		replaySameOptional(location, desired.occurrence.Location) && replaySameOptional(kind, desired.occurrence.CalendarKind) &&
		replaySameOptional(notes, desired.occurrence.CalendarNotes) && replaySameOptional(noteID, desired.occurrence.OccurrenceNoteID) &&
		replaySameDate(allDayEnd, desired.occurrence.AllDayEndDate), nil
}

func (a *ReplayProjectionApplier) insertNewProjection(ctx context.Context, tx *sql.Tx, event ReplayEvent) error {
	writer, err := NewV2ProjectionWriter(a.dialect)
	if err != nil {
		return err
	}
	writtenAt, err := replayProjectionWrittenAt(event.AfterImage)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	switch event.EntityKind {
	case ReplayEntityProject:
		return a.insertNewProject(ctx, tx, writer, event, writtenAt)
	case ReplayEntityTask:
		return a.insertNewTask(ctx, tx, writer, event, writtenAt)
	case ReplayEntityRule:
		return a.insertNewRule(ctx, tx, writer, event, writtenAt)
	case ReplayEntityOccurrence:
		return a.insertNewOccurrence(ctx, tx, writer, event, writtenAt)
	case ReplayEntityEvent:
		return a.insertNewCalendarEvent(ctx, tx, writer, event, writtenAt)
	default:
		return a.conflict(ReplayProjectionConflictTarget, event, "unsupported post-snapshot source kind")
	}
}

func replayProjectionWrittenAt(image ReplayImage) (string, error) {
	if value := replayOptionalImageField(image, "updated_at"); value != "" {
		parsed, err := replayTimeField(image, "updated_at", true)
		if err != nil {
			return "", err
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	}
	return time.Now().UTC().Format(time.RFC3339Nano), nil
}

func (a *ReplayProjectionApplier) migrationTimezone(ctx context.Context, tx *sql.Tx, event ReplayEvent) (string, error) {
	var timezone string
	err := tx.QueryRowContext(ctx, a.bind(`SELECT migration_timezone FROM workspace_task_domain_state WHERE workspace_id=?`), a.workspaceID).Scan(&timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", a.conflict(ReplayProjectionConflictDependency, event, "workspace migration state is absent")
	}
	if err != nil {
		return "", fmt.Errorf("load replay migration timezone: %w", err)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return "", a.conflict(ReplayProjectionConflictDependency, event, "invalid migration timezone="+timezone)
	}
	return timezone, nil
}

func (a *ReplayProjectionApplier) insertNewProject(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	writtenAt string,
) error {
	if event.SourceID == "system-inbox" {
		return a.conflict(ReplayProjectionConflictTarget, event, "reserved project ID")
	}
	name, err := replayRequiredImageField(event.AfterImage, "name")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	projectType, err := replayRequiredImageField(event.AfterImage, "type")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if LegacyProjectType(projectType) == LegacyProjectPersonal {
		return a.conflict(ReplayProjectionConflictTarget, event, "post-snapshot personal project needs preflight selection")
	}
	kind, horizon, err := replayProjectCapabilities(projectType)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	project := V2ProjectProjection{ID: event.SourceID, Name: name, Kind: kind, Horizon: horizon}
	if err := writer.writeProject(ctx, tx, a.workspaceID, project, writtenAt); err != nil {
		return fmt.Errorf("insert post-snapshot project: %w", err)
	}
	return a.insertNewMappings(ctx, tx, writer, event, V2IDMapEntry{
		LegacyKind: LegacyEntityProject, LegacyID: event.SourceID, TargetProjectID: event.SourceID,
	}, writtenAt)
}

func (a *ReplayProjectionApplier) legacyTaskImage(event ReplayEvent) (LegacyTaskRow, error) {
	image := event.AfterImage
	title, err := replayRequiredImageField(image, "title")
	if err != nil {
		return LegacyTaskRow{}, err
	}
	priority, err := replayIntField(image, "priority", 0, 3)
	if err != nil {
		return LegacyTaskRow{}, err
	}
	sortOrder, err := replayFloatField(image, "sort_order")
	if err != nil {
		return LegacyTaskRow{}, err
	}
	if math.Trunc(sortOrder) != sortOrder || sortOrder < math.MinInt64 || sortOrder > math.MaxInt64 {
		// The existing deterministic mapper currently stores an integral sort
		// order. A fractional post-snapshot source must stop rather than silently
		// changing order semantics; the mapper can be widened independently.
		return LegacyTaskRow{}, fmt.Errorf("fractional sort_order is not representable by the migration mapper")
	}
	status, err := replayStatus(replayOptionalImageField(image, "status"))
	if err != nil {
		return LegacyTaskRow{}, err
	}
	done, err := replayBoolField(image, "done")
	if err != nil {
		return LegacyTaskRow{}, err
	}
	if done {
		status = taskdomain.ExecutionStatusDone
	}
	planned, err := replayDateField(image, "planned_date", false)
	if err != nil {
		return LegacyTaskRow{}, err
	}
	due, err := replayTimeFieldAliases(image, false, "due_at", "due")
	if err != nil {
		return LegacyTaskRow{}, err
	}
	completed, err := replayTimeField(image, "completed_at", false)
	if err != nil {
		return LegacyTaskRow{}, err
	}
	updated, err := replayTimeField(image, "updated_at", true)
	if err != nil {
		return LegacyTaskRow{}, err
	}
	executionType := LegacyExecutionType(replayOptionalImageField(image, "execution_type"))
	if executionType == "" {
		executionType = LegacyExecutionSingle
	}
	return LegacyTaskRow{
		ID: event.SourceID, ProjectID: replayOptionalImageField(image, "project_id"), RoadmapNodeID: replayOptionalImageField(image, "roadmap_node_id"), ExecutionType: executionType,
		Title: title, Content: replayOptionalImageField(image, "content"), Priority: int(priority), SortOrder: int64(sortOrder),
		PlannedDate: planned, DueAt: due, Status: status, Done: done, CompletedAt: completed, UpdatedAt: *updated,
		NoteID: replayOptionalImageField(image, "note_id"),
	}, nil
}

func (a *ReplayProjectionApplier) insertNewTask(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	writtenAt string,
) error {
	legacy, err := a.legacyTaskImage(event)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	projectID, err := a.resolveProjectTarget(ctx, tx, event, legacy.ProjectID)
	if err != nil {
		return err
	}
	if legacy.RoadmapNodeID != "" {
		legacy.RoadmapNodeID, err = a.resolveMappedTarget(ctx, tx, event, ReplayEntityRoadmapNode, legacy.RoadmapNodeID, "roadmap_node")
		if err != nil {
			return err
		}
		var nodeProjectID string
		err = tx.QueryRowContext(ctx, a.bind(`SELECT project_id FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=?`), a.workspaceID, legacy.RoadmapNodeID).Scan(&nodeProjectID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && nodeProjectID != projectID) {
			return a.conflict(ReplayProjectionConflictDependency, event, "roadmap node is absent or belongs to another project")
		}
		if err != nil {
			return fmt.Errorf("resolve new replay task roadmap node: %w", err)
		}
	}
	entry := V2IDMapEntry{LegacyKind: LegacyEntityTask, LegacyID: event.SourceID, TargetProjectID: projectID, TargetTaskID: event.SourceID}
	switch legacy.ExecutionType {
	case LegacyExecutionSingle:
		timezone, err := a.migrationTimezone(ctx, tx, event)
		if err != nil {
			return err
		}
		task, schedule, occurrence, err := mapSingleTask(legacy, projectID, timezone)
		if err != nil {
			return err
		}
		entry.TargetScheduleID, entry.TargetOccurrenceID = schedule.TaskID, occurrence.ID
		if err := writer.writeTask(ctx, tx, a.workspaceID, task, writtenAt); err != nil {
			return err
		}
		if err := writer.writeScheduleHeader(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
			return err
		}
		if err := writer.writeScheduleVersion(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
			return err
		}
		if err := writer.writeOccurrence(ctx, tx, a.workspaceID, occurrence, writtenAt); err != nil {
			return err
		}
	case LegacyExecutionRecurring:
		task := V2TaskProjection{
			ID: event.SourceID, ProjectID: projectID, RoadmapNodeID: legacy.RoadmapNodeID, Title: legacy.Title, Description: legacy.Content,
			Priority: legacy.Priority, SortOrder: legacy.SortOrder, TaskNoteID: legacy.NoteID, LifecycleStatus: taskdomain.TaskLifecycleActive,
		}
		if err := writer.writeTask(ctx, tx, a.workspaceID, task, writtenAt); err != nil {
			return err
		}
	default:
		return a.conflict(ReplayProjectionConflictTarget, event, "unsupported execution_type="+string(legacy.ExecutionType))
	}
	return a.insertNewMappings(ctx, tx, writer, event, entry, writtenAt)
}

func (a *ReplayProjectionApplier) legacyRuleImage(event ReplayEvent, targetTaskID, fallbackTimezone string) (V2ScheduleProjection, error) {
	enabled, err := replayBoolField(event.AfterImage, "enabled")
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	if !enabled {
		return V2ScheduleProjection{}, errors.New("disabled recurrence rule requires an explicit task command")
	}
	frequency, err := replayRequiredImageField(event.AfterImage, "frequency")
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	recurrence := taskdomain.RecurrenceType(frequency)
	if recurrence != taskdomain.RecurrenceDaily && recurrence != taskdomain.RecurrenceWeekly && recurrence != taskdomain.RecurrenceMonthly {
		return V2ScheduleProjection{}, fmt.Errorf("unsupported frequency=%s", frequency)
	}
	interval, err := replayIntField(event.AfterImage, "interval", 1, math.MaxInt32)
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	weekdays, err := replayIntArrayField(event.AfterImage, "weekdays")
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	monthDays, err := replayIntArrayField(event.AfterImage, "month_days")
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	startsOn, err := replayDateField(event.AfterImage, "start_date", true)
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	endsOn, err := replayDateField(event.AfterImage, "end_date", false)
	if err != nil {
		return V2ScheduleProjection{}, err
	}
	timezone := replayOptionalImageField(event.AfterImage, "timezone")
	if timezone == "" {
		timezone = fallbackTimezone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return V2ScheduleProjection{}, fmt.Errorf("invalid timezone=%s", timezone)
	}
	return V2ScheduleProjection{
		TaskID: targetTaskID, RecurrenceType: recurrence, TimingType: taskdomain.TimingDate, Timezone: timezone,
		StartsOn: startsOn, EndsOn: endsOn, Interval: int(interval), Weekdays: weekdays, MonthDays: monthDays,
	}, nil
}

func (a *ReplayProjectionApplier) insertNewRule(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	writtenAt string,
) error {
	targetTaskID, err := a.resolveMappedTarget(ctx, tx, event, ReplayEntityTask, event.SourceID, "task")
	if err != nil {
		return err
	}
	timezone, err := a.migrationTimezone(ctx, tx, event)
	if err != nil {
		return err
	}
	schedule, err := a.legacyRuleImage(event, targetTaskID, timezone)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	if err := writer.writeScheduleHeader(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
		return err
	}
	if err := writer.writeScheduleVersion(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
		return err
	}
	return a.insertNewMappings(ctx, tx, writer, event, V2IDMapEntry{
		LegacyKind: LegacyEntityRule, LegacyID: event.SourceID, TargetTaskID: targetTaskID, TargetScheduleID: targetTaskID,
	}, writtenAt)
}

func (a *ReplayProjectionApplier) insertNewOccurrence(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	writtenAt string,
) error {
	legacyTaskID, _ := replayRequiredImageField(event.AfterImage, "task_id")
	targetTaskID, err := a.resolveMappedTarget(ctx, tx, event, ReplayEntityTask, legacyTaskID, "task")
	if err != nil {
		return err
	}
	if _, err := a.resolveMappedTarget(ctx, tx, event, ReplayEntityRule, legacyTaskID, "schedule"); err != nil {
		return err
	}
	planned, err := replayDateField(event.AfterImage, "occurrence_date", true)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	status, err := replayStatus(replayOptionalImageField(event.AfterImage, "status"))
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	completed, err := replayTimeField(event.AfterImage, "completed_at", false)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	updated, err := replayTimeField(event.AfterImage, "updated_at", true)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	legacy := LegacyOccurrenceRow{TaskID: legacyTaskID, OccurrenceDate: planned, Status: status, CompletedAt: completed, UpdatedAt: *updated}
	occurrence, err := mapRecurringOccurrence(LegacyTaskRow{}, legacy)
	if err != nil {
		return err
	}
	occurrence.TaskID = targetTaskID
	occurrence.CalendarNotes = replayOptionalImageField(event.AfterImage, "note")
	if err := writer.writeOccurrence(ctx, tx, a.workspaceID, occurrence, writtenAt); err != nil {
		return err
	}
	return a.insertNewMappings(ctx, tx, writer, event, V2IDMapEntry{
		LegacyKind: LegacyEntityOccurrence, LegacyID: event.SourceID, TargetTaskID: targetTaskID, TargetOccurrenceID: occurrence.ID,
	}, writtenAt)
}

func (a *ReplayProjectionApplier) insertNewCalendarEvent(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	writtenAt string,
) error {
	projectID, err := a.resolveProjectTarget(ctx, tx, event, replayOptionalImageField(event.AfterImage, "project_id"))
	if err != nil {
		return err
	}
	timezone, err := a.migrationTimezone(ctx, tx, event)
	if err != nil {
		return err
	}
	location, _ := time.LoadLocation(timezone)
	start, err := replayTimeFieldAliases(event.AfterImage, true, "start_time", "start_at")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	end, err := replayTimeFieldAliases(event.AfterImage, true, "end_time", "end_at")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	allDay, err := replayBoolField(event.AfterImage, "is_all_day")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	title, err := replayRequiredImageField(event.AfterImage, "title")
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	task, schedule, occurrence, err := mapLegacyEvent(LegacyEventRow{
		ID: event.SourceID, ProjectID: replayOptionalImageField(event.AfterImage, "project_id"), Title: title,
		StartAt: *start, EndAt: *end, AllDay: allDay, Location: replayOptionalImageField(event.AfterImage, "location"),
		Kind: replayOptionalImageField(event.AfterImage, "kind"), Notes: replayOptionalImageField(event.AfterImage, "notes"), NoteID: replayOptionalImageField(event.AfterImage, "note_id"),
	}, projectID, timezone, location)
	if err != nil {
		return err
	}
	schedule, err = normalizeProjectedSchedule(schedule, []V2OccurrenceProjection{occurrence})
	if err != nil {
		return err
	}
	if err := writer.writeTask(ctx, tx, a.workspaceID, task, writtenAt); err != nil {
		return err
	}
	if err := writer.writeScheduleHeader(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
		return err
	}
	if err := writer.writeScheduleVersion(ctx, tx, a.workspaceID, schedule, writtenAt); err != nil {
		return err
	}
	if err := writer.writeOccurrence(ctx, tx, a.workspaceID, occurrence, writtenAt); err != nil {
		return err
	}
	return a.insertNewMappings(ctx, tx, writer, event, V2IDMapEntry{
		LegacyKind: LegacyEntityEvent, LegacyID: event.SourceID, TargetProjectID: projectID,
		TargetTaskID: task.ID, TargetScheduleID: schedule.TaskID, TargetOccurrenceID: occurrence.ID,
	}, writtenAt)
}

func (a *ReplayProjectionApplier) insertNewMappings(
	ctx context.Context,
	tx *sql.Tx,
	writer *V2ProjectionWriter,
	event ReplayEvent,
	entry V2IDMapEntry,
	writtenAt string,
) error {
	targets := projectionMapTargets(entry)
	if len(targets) == 0 {
		return a.conflict(ReplayProjectionConflictMapping, event, "new projection has no targets")
	}
	for _, target := range targets {
		if err := writer.writeIDMap(ctx, tx, a.workspaceID, entry, target, event.LogicalVersion, writtenAt); err != nil {
			return err
		}
	}
	return nil
}

func (a *ReplayProjectionApplier) recordUnmaterializedTombstone(ctx context.Context, tx *sql.Tx, event ReplayEvent) error {
	image := event.TombstoneImage
	entry := V2IDMapEntry{LegacyKind: LegacyEntityKind(event.EntityKind), LegacyID: event.SourceID}
	switch event.EntityKind {
	case ReplayEntityProject:
		entry.TargetProjectID = event.SourceID
	case ReplayEntityTask:
		entry.TargetTaskID = event.SourceID
		executionType := LegacyExecutionType(replayOptionalImageField(image, "execution_type"))
		if executionType == "" || executionType == LegacyExecutionSingle {
			entry.TargetScheduleID = event.SourceID
			entry.TargetOccurrenceID = deterministicProjectionID("task-occurrence", event.SourceID, "once")
		} else if executionType != LegacyExecutionRecurring {
			return a.conflict(ReplayProjectionConflictTarget, event, "unsupported tombstone execution_type="+string(executionType))
		}
	case ReplayEntityRule:
		entry.TargetTaskID = event.SourceID
		entry.TargetScheduleID = event.SourceID
	case ReplayEntityOccurrence:
		legacyTaskID, err := replayRequiredImageField(image, "task_id")
		if err != nil {
			return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
		}
		occurrenceDate, err := replayDateField(image, "occurrence_date", true)
		if err != nil {
			return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
		}
		entry.TargetTaskID = legacyTaskID
		entry.TargetOccurrenceID = deterministicProjectionID("task-occurrence", legacyTaskID, occurrenceDate)
	case ReplayEntityEvent:
		entry.TargetTaskID = deterministicProjectionID("event-task", event.SourceID)
		entry.TargetScheduleID = entry.TargetTaskID
		entry.TargetOccurrenceID = deterministicProjectionID("event-occurrence", event.SourceID)
	default:
		return a.conflict(ReplayProjectionConflictTarget, event, "unsupported tombstone source kind")
	}
	targets := projectionMapTargets(entry)
	if len(targets) == 0 {
		return a.conflict(ReplayProjectionConflictMapping, event, "tombstone has no deterministic targets")
	}
	writtenAt, err := replayProjectionWrittenAt(image)
	if err != nil {
		return a.conflict(ReplayProjectionConflictTarget, event, err.Error())
	}
	for _, target := range targets {
		query := a.bind(`INSERT INTO task_domain_legacy_id_map
			(workspace_id,entity_kind,legacy_id,target_kind,v2_id,source_logical_version,deleted,updated_at)
			VALUES(?,?,?,?,?,?,?,?)`)
		if _, err := tx.ExecContext(ctx, query, a.workspaceID, event.EntityKind, event.SourceID, target.kind, target.id,
			event.LogicalVersion, true, writtenAt); err != nil {
			return fmt.Errorf("insert unmaterialized replay tombstone %s/%s -> %s/%s: %w",
				event.EntityKind, event.SourceID, target.kind, target.id, err)
		}
	}
	return nil
}
