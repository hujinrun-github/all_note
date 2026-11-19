// Package workspacequerydb implements the portable, read-only SQL queries used
// by the SQLite and PostgreSQL workspace read capabilities.
package workspacequerydb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/workspacequery"
)

type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

type Beginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

type Scanner interface{ Scan(...any) error }
type OccurrenceScanner func(Scanner) (taskdomain.QueryOccurrenceSnapshot, error)

type Reader struct {
	db               Beginner
	workspaceID      string
	dialect          Dialect
	occurrenceSelect string
	scanOccurrence   OccurrenceScanner
}

func New(db Beginner, workspaceID string, dialect Dialect, occurrenceSelect string, scan OccurrenceScanner) *Reader {
	return &Reader{db: db, workspaceID: workspaceID, dialect: dialect, occurrenceSelect: occurrenceSelect, scanOccurrence: scan}
}

func (r *Reader) Overview(ctx context.Context, request workspacequery.Request) (workspacequery.PlanningOverview, error) {
	result := workspacequery.PlanningOverview{Today: []taskdomain.QueryOccurrenceSnapshot{}, Projects: []workspacequery.ProjectSummary{}, ProjectNames: map[string]string{}}
	if request.ExpectedEpoch < 1 || !validWindow(request.Window) || request.TodayLimit < 0 || request.TodayLimit > 50 || request.ProjectLimit < 0 || request.ProjectLimit > 20 {
		return result, workspacequery.ErrInvalidRequest
	}
	err := r.read(ctx, request.ExpectedEpoch, func(tx *sql.Tx) error {
		if request.ProjectID != "" {
			if err := r.requireProject(ctx, tx, request.ProjectID); err != nil {
				return err
			}
		}
		facets, err := r.facets(ctx, tx, request)
		if err != nil {
			return err
		}
		result.Facets = facets
		if request.TodayLimit > 0 {
			result.Today, err = r.today(ctx, tx, request)
			if err != nil {
				return err
			}
		}
		if len(result.Today) > 0 {
			result.ProjectNames, err = r.projectNames(ctx, tx, result.Today)
			if err != nil {
				return err
			}
		}
		if request.ProjectLimit > 0 {
			result.Projects, err = r.projects(ctx, tx, workspacequery.ProjectRequest{Window: request.Window, ExpectedEpoch: request.ExpectedEpoch, ProjectID: request.ProjectID, Limit: request.ProjectLimit}, true)
		}
		return err
	})
	return result, err
}

func (r *Reader) ProjectSummaries(ctx context.Context, request workspacequery.ProjectRequest) ([]workspacequery.ProjectSummary, error) {
	if request.ExpectedEpoch < 1 || !validWindow(request.Window) || request.Limit < 0 || taskdomain.ValidateProjectListFilter(request.Filter) != nil {
		return nil, workspacequery.ErrInvalidRequest
	}
	var result []workspacequery.ProjectSummary
	err := r.read(ctx, request.ExpectedEpoch, func(tx *sql.Tx) (err error) {
		result, err = r.projects(ctx, tx, request, false)
		return err
	})
	return result, err
}

func (r *Reader) ProjectOverview(ctx context.Context, request workspacequery.ProjectRequest) (workspacequery.ProjectSummary, error) {
	if strings.TrimSpace(request.ProjectID) == "" {
		return workspacequery.ProjectSummary{}, workspacequery.ErrInvalidRequest
	}
	items, err := r.ProjectSummaries(ctx, request)
	if err != nil {
		return workspacequery.ProjectSummary{}, err
	}
	if len(items) != 1 {
		return workspacequery.ProjectSummary{}, taskdomain.ErrProjectNotFound
	}
	return items[0], nil
}

// Every response is read from one database snapshot. PostgreSQL's default
// READ COMMITTED would otherwise allow facets and projects to see different
// commits even inside the same transaction.
func (r *Reader) read(ctx context.Context, epoch int64, fn func(*sql.Tx) error) error {
	if r == nil || r.db == nil || r.workspaceID == "" || epoch < 1 || r.scanOccurrence == nil || (r.dialect != SQLite && r.dialect != Postgres) {
		return workspacequery.ErrRuntimeUnavailable
	}
	options := &sql.TxOptions{ReadOnly: true}
	if r.dialect == Postgres {
		options.Isolation = sql.LevelRepeatableRead
	}
	tx, err := r.db.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("%w: begin read: %v", workspacequery.ErrRuntimeUnavailable, err)
	}
	defer tx.Rollback()
	var anchorEpoch, domainEpoch int64
	var state, model, migration string
	var legacyWrites bool
	err = tx.QueryRowContext(ctx, r.bind(`SELECT a.epoch,a.state,s.model_version,s.migration_state,s.write_epoch,s.accept_legacy_writes
		FROM tenant_workspaces a JOIN workspace_task_domain_state s ON s.workspace_id=a.workspace_id
		WHERE a.workspace_id=?`), r.workspaceID).Scan(&anchorEpoch, &state, &model, &migration, &domainEpoch, &legacyWrites)
	if err != nil {
		return fmt.Errorf("%w: workspace state: %v", workspacequery.ErrRuntimeUnavailable, err)
	}
	if state != "active" || model != "v2" || (migration != "idle" && migration != "cutover") || legacyWrites || anchorEpoch != epoch || domainEpoch != epoch {
		return workspacequery.ErrRuntimeUnavailable
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Reader) requireProject(ctx context.Context, tx *sql.Tx, id string) error {
	var found string
	err := tx.QueryRowContext(ctx, r.bind(`SELECT id FROM domain_projects_v2 WHERE workspace_id=? AND id=?`), r.workspaceID, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return taskdomain.ErrProjectNotFound
	}
	return err
}

const occurrenceJoins = ` FROM domain_task_occurrences_v2 o
	JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
	JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
	JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=o.workspace_id AND v.task_id=o.task_id AND v.schedule_revision=o.generated_schedule_revision`

const pending = `(o.execution_status IN ('open','active','blocked') AND t.lifecycle_status IN ('active','paused') AND p.status IN ('planning','active','paused'))`

func (r *Reader) facets(ctx context.Context, tx *sql.Tx, request workspacequery.Request) (workspacequery.Facets, error) {
	w := request.Window
	args := []any{}
	add := func(sql string, values ...any) string { args = append(args, values...); return sql }
	today := add(r.overlap(), localDate(w.End, w.Timezone), w.Date, r.instant(w.End), r.instant(w.Start))
	week := add(r.starts(), localDate(w.WeekStart, w.Timezone), localDate(w.WeekEnd, w.Timezone), r.instant(w.WeekStart), r.instant(w.WeekEnd))
	month := add(r.starts(), localDate(w.MonthStart, w.Timezone), localDate(w.MonthEnd, w.Timezone), r.instant(w.MonthStart), r.instant(w.MonthEnd))
	overdue := add(r.timeColumn("o.due_at")+` < `+r.timeParameter(), r.instant(w.Start))
	completedToday := add(`o.execution_status='done' AND `+r.timeColumn("o.completed_at")+` >= `+r.timeParameter()+` AND `+r.timeColumn("o.completed_at")+` < `+r.timeParameter(), r.instant(w.Start), r.instant(w.End))
	completedWeek := add(`o.execution_status='done' AND `+r.timeColumn("o.completed_at")+` >= `+r.timeParameter()+` AND `+r.timeColumn("o.completed_at")+` < `+r.timeParameter(), r.instant(w.WeekStart), r.instant(w.WeekEnd))
	where := `o.workspace_id=?`
	args = append(args, r.workspaceID)
	if request.ProjectID != "" {
		where += ` AND t.project_id=?`
		args = append(args, request.ProjectID)
	}
	count := func(predicate string) string { return `COALESCE(SUM(CASE WHEN ` + predicate + ` THEN 1 ELSE 0 END),0)` }
	query := `SELECT ` + strings.Join([]string{
		count(pending + ` AND ` + today), count(pending + ` AND ` + week), count(pending + ` AND ` + month), count(pending + ` AND ` + overdue),
		count(pending + ` AND o.planned_date IS NULL AND o.planned_start_at IS NULL`), count(pending + ` AND p.system_role='inbox'`),
		count(completedToday), count(completedWeek), count(`o.execution_status='done'`), count(pending + ` AND v.recurrence_type<>'none'`),
	}, ",") + occurrenceJoins + ` WHERE ` + where
	var result workspacequery.Facets
	err := tx.QueryRowContext(ctx, r.bind(query), args...).Scan(&result.Today, &result.Week, &result.Month, &result.Overdue, &result.Unscheduled, &result.Inbox, &result.CompletedToday, &result.CompletedWeek, &result.CompletedTotal, &result.Recurring)
	if err != nil {
		return result, err
	}
	draftQuery := `SELECT COUNT(*) FROM domain_tasks_v2 t JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		WHERE t.workspace_id=? AND t.lifecycle_status='draft' AND p.status IN ('planning','active','paused')`
	draftArgs := []any{r.workspaceID}
	if request.ProjectID != "" {
		draftQuery += ` AND t.project_id=?`
		draftArgs = append(draftArgs, request.ProjectID)
	}
	err = tx.QueryRowContext(ctx, r.bind(draftQuery), draftArgs...).Scan(&result.Draft)
	return result, err
}

func (r *Reader) today(ctx context.Context, tx *sql.Tx, request workspacequery.Request) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	w := request.Window
	query := r.occurrenceSelect + ` WHERE o.workspace_id=? AND ` + pending + ` AND ` + r.overlap()
	args := []any{r.workspaceID, localDate(w.End, w.Timezone), w.Date, r.instant(w.End), r.instant(w.Start)}
	if request.ProjectID != "" {
		query += ` AND t.project_id=?`
		args = append(args, request.ProjectID)
	}
	// Timed execution is chronological; flexible date work follows priority and
	// the user's ordering. The ID is the final deterministic tie-breaker.
	query += ` ORDER BY CASE WHEN o.planned_start_at IS NOT NULL THEN 0 ELSE 1 END,` + r.timeColumn("o.planned_start_at") + `,
		t.priority DESC,t.sort_order,o.id LIMIT ?`
	args = append(args, request.TodayLimit)
	return r.occurrences(ctx, tx, query, args)
}

func (r *Reader) projects(ctx context.Context, tx *sql.Tx, request workspacequery.ProjectRequest, overview bool) ([]workspacequery.ProjectSummary, error) {
	selected, args := r.selectedProjects(request, overview)
	query := `WITH selected_projects AS (` + selected + `), task_totals AS (
		SELECT t.project_id,
		COALESCE(SUM(CASE WHEN t.lifecycle_status='completed' THEN 1 ELSE 0 END),0) AS completed,
		COALESCE(SUM(CASE WHEN t.lifecycle_status IN ('active','paused','completed') THEN 1 ELSE 0 END),0) AS actionable,
		MAX(CASE WHEN t.lifecycle_status IN ('active','paused') AND v.recurrence_type<>'none' AND v.ends_on IS NULL THEN 1 ELSE 0 END) AS open_ended
		FROM domain_tasks_v2 t JOIN selected_projects p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		JOIN domain_task_schedule_versions_v2 v ON v.workspace_id=s.workspace_id AND v.task_id=s.task_id AND v.schedule_revision=s.current_schedule_revision
		GROUP BY t.project_id
	), occurrence_totals AS (
		SELECT t.project_id,
		COALESCE(SUM(CASE WHEN ` + pending + ` THEN 1 ELSE 0 END),0) AS open_count,
		COALESCE(SUM(CASE WHEN ` + pending + ` AND o.execution_status='blocked' THEN 1 ELSE 0 END),0) AS blocked_count
		FROM domain_task_occurrences_v2 o JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
		JOIN selected_projects p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		GROUP BY t.project_id
	)
	SELECT p.workspace_id,p.id,p.name,p.kind,p.horizon,p.status,p.archived_from_status,p.system_role,p.revision,
	COALESCE(t.completed,0),COALESCE(t.actionable,0),COALESCE(o.open_count,0),COALESCE(o.blocked_count,0),COALESCE(t.open_ended,0)
	FROM selected_projects p LEFT JOIN task_totals t ON t.project_id=p.id LEFT JOIN occurrence_totals o ON o.project_id=p.id
	ORDER BY p.name,p.id`
	rows, err := tx.QueryContext(ctx, r.bind(query), args...)
	if err != nil {
		return nil, err
	}
	items := []workspacequery.ProjectSummary{}
	for rows.Next() {
		var item workspacequery.ProjectSummary
		var archived, role sql.NullString
		var openEnded int
		err = rows.Scan(&item.Project.Project.WorkspaceID, &item.Project.Project.ID, &item.Project.Project.Name, &item.Project.Project.Kind, &item.Project.Project.Horizon, &item.Project.Project.Status, &archived, &role, &item.Project.Revision,
			&item.CompletedTaskCount, &item.TotalActionableTaskCount, &item.OpenOccurrenceCount, &item.BlockedOccurrenceCount, &openEnded)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if archived.Valid {
			value := taskdomain.ProjectStatus(archived.String)
			item.Project.Project.ArchivedFromStatus = &value
		}
		item.Project.Project.SystemRole = taskdomain.ProjectSystemRole(role.String)
		item.HasOpenEndedRecurrence = openEnded != 0
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(items) == 0 {
		return items, err
	}
	// Rank once across the selected catalog and fetch one canonical snapshot per
	// project in a single query; this avoids per-project GetOccurrence requests.
	nextQuery := `WITH selected_projects AS (` + selected + `), ranked_next AS (
		SELECT o.id,ROW_NUMBER() OVER (PARTITION BY t.project_id ORDER BY CASE WHEN o.planned_date IS NULL THEN 1 ELSE 0 END,o.planned_date,
		CASE WHEN o.planned_start_at IS NULL THEN 0 ELSE 1 END,` + r.timeColumn("o.planned_start_at") + `,t.priority DESC,t.sort_order,o.id) AS position
		FROM domain_task_occurrences_v2 o JOIN domain_tasks_v2 t ON t.workspace_id=o.workspace_id AND t.id=o.task_id
		JOIN selected_projects p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		WHERE ` + pending + ` AND ((o.planned_start_at IS NOT NULL AND ` + r.timeColumn("o.planned_end_at") + `>` + r.timeParameter() + `)
		OR (o.planned_start_at IS NULL AND o.planned_date IS NOT NULL AND ` + r.dateEnd() + `>?))
	) ` + r.occurrenceSelect + ` JOIN ranked_next n ON n.id=o.id AND n.position=1 WHERE o.workspace_id=?`
	nextArgs := append(append([]any{}, args...), r.instant(request.Window.Start), request.Window.Date, r.workspaceID)
	next, err := r.occurrences(ctx, tx, nextQuery, nextArgs)
	if err != nil {
		return nil, err
	}
	byProject := make(map[string]taskdomain.QueryOccurrenceSnapshot, len(next))
	for _, item := range next {
		byProject[item.ProjectID] = item
	}
	for i := range items {
		if next, ok := byProject[items[i].Project.Project.ID]; ok {
			items[i].NextOccurrence = &next
		}
	}
	return items, nil
}

func (r *Reader) selectedProjects(request workspacequery.ProjectRequest, overview bool) (string, []any) {
	query := `SELECT p.workspace_id,p.id,p.name,p.kind,p.horizon,p.status,p.archived_from_status,p.system_role,p.revision FROM domain_projects_v2 p WHERE p.workspace_id=?`
	args := []any{r.workspaceID}
	if overview {
		query += ` AND p.status<>'archived' AND COALESCE(p.system_role,'')<>'inbox'`
	}
	if request.ProjectID != "" {
		query += ` AND p.id=?`
		args = append(args, request.ProjectID)
	}
	if request.Filter.Kind != nil {
		query += ` AND p.kind=?`
		args = append(args, *request.Filter.Kind)
	}
	if request.Filter.Horizon != nil {
		query += ` AND p.horizon=?`
		args = append(args, *request.Filter.Horizon)
	}
	if request.Filter.Status != nil {
		query += ` AND p.status=?`
		args = append(args, *request.Filter.Status)
	}
	query += ` ORDER BY p.name,p.id`
	if request.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, request.Limit)
	}
	return query, args
}

func (r *Reader) projectNames(ctx context.Context, tx *sql.Tx, items []taskdomain.QueryOccurrenceSnapshot) (map[string]string, error) {
	result := make(map[string]string)
	args := []any{r.workspaceID}
	placeholders := []string{}
	for _, item := range items {
		if _, ok := result[item.ProjectID]; !ok {
			result[item.ProjectID] = ""
			args = append(args, item.ProjectID)
			placeholders = append(placeholders, "?")
		}
	}
	rows, err := tx.QueryContext(ctx, r.bind(`SELECT id,name FROM domain_projects_v2 WHERE workspace_id=? AND id IN (`+strings.Join(placeholders, ",")+`)`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		result[id] = name
	}
	return result, rows.Err()
}

func (r *Reader) occurrences(ctx context.Context, tx *sql.Tx, query string, args []any) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	rows, err := tx.QueryContext(ctx, r.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []taskdomain.QueryOccurrenceSnapshot{}
	for rows.Next() {
		item, err := r.scanOccurrence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Reader) overlap() string {
	return `((o.planned_start_at IS NULL AND o.planned_date<? AND ` + r.dateEnd() + `>?) OR (o.planned_start_at IS NOT NULL AND ` + r.timeColumn("o.planned_start_at") + `<` + r.timeParameter() + ` AND ` + r.timeColumn("o.planned_end_at") + `>` + r.timeParameter() + `))`
}
func (r *Reader) starts() string {
	return `((o.planned_start_at IS NULL AND o.planned_date>=? AND o.planned_date<?) OR (o.planned_start_at IS NOT NULL AND ` + r.timeColumn("o.planned_start_at") + `>=` + r.timeParameter() + ` AND ` + r.timeColumn("o.planned_start_at") + `<` + r.timeParameter() + `))`
}
func (r *Reader) dateEnd() string {
	if r.dialect == Postgres {
		return `COALESCE(o.all_day_end_date,o.planned_date+1)`
	}
	return `COALESCE(o.all_day_end_date,date(o.planned_date,'+1 day'))`
}

// SQLite persists RFC3339 timestamps with variable fractional precision and
// offsets. Normalize the whole seconds independently from the original padded
// fraction: julianday loses microseconds and strftime on the complete value can
// round a value just before midnight into the next day.
func (r *Reader) timeColumn(column string) string {
	if r.dialect == SQLite {
		zone := `(CASE WHEN substr(` + column + `,-1)='Z' THEN 'Z' ELSE substr(` + column + `,-6) END)`
		fraction := `(CASE WHEN substr(` + column + `,20,1)='.' THEN substr(` + column + `,21,length(` + column + `)-20-length(` + zone + `)) ELSE '' END)`
		return `(strftime('%Y-%m-%dT%H:%M:%S',substr(` + column + `,1,19)||` + zone + `)||'.'||substr(` + fraction + `||'000000000',1,9)||'Z')`
	}
	return column
}
func (r *Reader) timeParameter() string {
	return "?"
}
func (r *Reader) instant(value time.Time) any {
	if r.dialect == Postgres {
		return value.UTC()
	}
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
func (r *Reader) bind(query string) string {
	if r.dialect != Postgres {
		return query
	}
	var result strings.Builder
	index := 1
	for _, c := range query {
		if c == '?' {
			fmt.Fprintf(&result, "$%d", index)
			index++
		} else {
			result.WriteRune(c)
		}
	}
	return result.String()
}
func localDate(value time.Time, timezone string) string {
	location, _ := time.LoadLocation(timezone)
	return value.In(location).Format("2006-01-02")
}
func validWindow(w workspacequery.Window) bool {
	if w.Timezone == "" || w.Timezone == "Local" {
		return false
	}
	location, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return false
	}
	start, err := time.ParseInLocation("2006-01-02", w.Date, location)
	if err != nil {
		return false
	}
	week := start.AddDate(0, 0, -(int(start.Weekday())+6)%7)
	month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, location)
	return w.Start.Equal(start) && w.End.Equal(start.AddDate(0, 0, 1)) && w.WeekStart.Equal(week) && w.WeekEnd.Equal(week.AddDate(0, 0, 7)) && w.MonthStart.Equal(month) && w.MonthEnd.Equal(month.AddDate(0, 1, 0))
}

var _ workspacequery.Reader = (*Reader)(nil)
