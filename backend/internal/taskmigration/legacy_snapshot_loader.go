package taskmigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

var ErrInvalidLegacySnapshotLoader = errors.New("invalid legacy task-domain snapshot loader")

type LegacySnapshotSchemaErrorCode string

const (
	LegacySnapshotMissingTable  LegacySnapshotSchemaErrorCode = "missing_table"
	LegacySnapshotMissingColumn LegacySnapshotSchemaErrorCode = "missing_column"
)

// LegacySnapshotSchemaError is fail-closed: a migration must stop instead of
// silently projecting a partial historical schema.
type LegacySnapshotSchemaError struct {
	Code   LegacySnapshotSchemaErrorCode
	Table  string
	Column string
}

func (e *LegacySnapshotSchemaError) Error() string {
	if e.Column == "" {
		return fmt.Sprintf("legacy task-domain snapshot schema: %s: table=%s", e.Code, e.Table)
	}
	return fmt.Sprintf("legacy task-domain snapshot schema: %s: table=%s column=%s", e.Code, e.Table, e.Column)
}

type LegacySnapshotLoaderConfig struct {
	Dialect            Dialect
	WorkspaceID        string
	WorkspaceTimezone  string
	OwnerTimezone      string
	DeploymentTimezone string
}

// LegacySnapshotLoader reads only through the transaction supplied by the
// backfill coordinator. It never opens, commits, or rolls back a transaction.
type LegacySnapshotLoader struct {
	config LegacySnapshotLoaderConfig
}

func NewLegacySnapshotLoader(config LegacySnapshotLoaderConfig) (*LegacySnapshotLoader, error) {
	config.WorkspaceID = strings.TrimSpace(config.WorkspaceID)
	config.WorkspaceTimezone = strings.TrimSpace(config.WorkspaceTimezone)
	config.OwnerTimezone = strings.TrimSpace(config.OwnerTimezone)
	config.DeploymentTimezone = strings.TrimSpace(config.DeploymentTimezone)
	if config.WorkspaceID == "" || (config.Dialect != DialectSQLite && config.Dialect != DialectPostgres) {
		return nil, fmt.Errorf("%w: workspace and supported dialect are required", ErrInvalidLegacySnapshotLoader)
	}
	return &LegacySnapshotLoader{config: config}, nil
}

type legacySnapshotLayout struct {
	Sources                 map[LegacySourceKind][]string
	TaskDueColumn           string
	TaskRoadmapNodeColumn   bool
	EventStartColumn        string
	EventEndColumn          string
	ProjectDeleted          bool
	TaskDeleted             bool
	OccurrenceDeleted       bool
	EventDeleted            bool
	RoadmapNodePositionJSON bool
}

// Load inventories and reads the source inside one caller-owned consistent
// snapshot. Every entity query is workspace-qualified and deterministically
// ordered.
func (l *LegacySnapshotLoader) Load(ctx context.Context, tx *sql.Tx) (LegacyTaskDomainInventory, LegacyTaskDomainRows, error) {
	if l == nil || ctx == nil || tx == nil || strings.TrimSpace(l.config.WorkspaceID) == "" {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, fmt.Errorf("%w: loader, context, and transaction are required", ErrInvalidLegacySnapshotLoader)
	}
	tables, err := inspectLegacySnapshotSchema(ctx, tx, l.config.Dialect)
	if err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	layout, err := buildLegacySnapshotLayout(l.config.Dialect, tables)
	if err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	inventory := LegacyTaskDomainInventory{
		WorkspaceID:        l.config.WorkspaceID,
		WorkspaceTimezone:  l.config.WorkspaceTimezone,
		OwnerTimezone:      l.config.OwnerTimezone,
		DeploymentTimezone: l.config.DeploymentTimezone,
		Sources:            cloneLegacySnapshotSources(layout.Sources),
	}
	var rows LegacyTaskDomainRows
	if err := l.loadProjects(ctx, tx, layout, &inventory, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	if err := l.loadRoadmaps(ctx, tx, layout, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	if err := l.loadTasks(ctx, tx, layout, &inventory, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	if err := l.loadRules(ctx, tx, layout, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	if err := l.loadOccurrences(ctx, tx, layout, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	if err := l.loadEvents(ctx, tx, layout, &inventory, &rows); err != nil {
		return LegacyTaskDomainInventory{}, LegacyTaskDomainRows{}, err
	}
	return inventory, rows, nil
}

func inspectLegacySnapshotSchema(ctx context.Context, tx *sql.Tx, dialect Dialect) (map[string][]string, error) {
	tables := []string{"task_projects", "tasks", "task_recurrence_rules", "task_occurrences", "events", "learning_roadmaps", "roadmap_nodes", "roadmap_edges"}
	result := make(map[string][]string, len(tables))
	for _, table := range tables {
		var (
			rows *sql.Rows
			err  error
		)
		if dialect == DialectSQLite {
			rows, err = tx.QueryContext(ctx, `PRAGMA table_info(`+quoteLegacySnapshotIdentifier(table)+`)`)
		} else {
			rows, err = tx.QueryContext(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, table)
		}
		if err != nil {
			return nil, fmt.Errorf("inspect legacy snapshot table %s: %w", table, err)
		}
		if dialect == DialectSQLite {
			for rows.Next() {
				var cid int
				var name, dataType string
				var notNull, primaryKey int
				var defaultValue any
				if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("scan legacy snapshot table %s: %w", table, err)
				}
				result[table] = append(result[table], name)
			}
		} else {
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("scan legacy snapshot table %s: %w", table, err)
				}
				result[table] = append(result[table], name)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate legacy snapshot table %s: %w", table, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close legacy snapshot table %s inventory: %w", table, err)
		}
	}
	return result, nil
}

func buildLegacySnapshotLayout(dialect Dialect, tables map[string][]string) (legacySnapshotLayout, error) {
	if dialect != DialectSQLite && dialect != DialectPostgres {
		return legacySnapshotLayout{}, fmt.Errorf("%w: unsupported dialect %q", ErrInvalidLegacySnapshotLoader, dialect)
	}
	columns := make(map[string]map[string]struct{}, len(tables))
	for table, available := range tables {
		columns[table] = make(map[string]struct{}, len(available))
		for _, column := range available {
			columns[table][strings.TrimSpace(column)] = struct{}{}
		}
	}
	require := func(table string, required ...string) error {
		available, exists := columns[table]
		if !exists || len(available) == 0 {
			return &LegacySnapshotSchemaError{Code: LegacySnapshotMissingTable, Table: table}
		}
		for _, column := range required {
			if _, exists := available[column]; !exists {
				return &LegacySnapshotSchemaError{Code: LegacySnapshotMissingColumn, Table: table, Column: column}
			}
		}
		return nil
	}
	if err := require("task_projects", "id", "workspace_id", "name", "type", "created_at"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("tasks", "id", "workspace_id", "project_id", "execution_type", "title", "content", "priority", "sort_order", "planned_date", "status", "done", "completed_at", "updated_at", "note_id"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("task_recurrence_rules", "task_id", "workspace_id", "start_date", "end_date", "frequency", "interval", "weekdays", "month_days", "timezone", "enabled", "updated_at"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("task_occurrences", "task_id", "workspace_id", "occurrence_date", "status", "completed_at", "updated_at", "note"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("events", "id", "workspace_id", "title", "is_all_day", "project_id", "location", "kind", "notes", "note_id"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("learning_roadmaps", "id", "workspace_id", "project_id", "title", "goal", "status", "created_at", "updated_at"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("roadmap_nodes", "id", "workspace_id", "roadmap_id", "parent_id", "type", "title", "description", "path_type", "status", "deliverable", "acceptance_criteria", "order_index", "article_search_queries", "created_at", "updated_at"); err != nil {
		return legacySnapshotLayout{}, err
	}
	if err := require("roadmap_edges", "id", "workspace_id", "roadmap_id", "source_node_id", "target_node_id", "style", "created_at"); err != nil {
		return legacySnapshotLayout{}, err
	}

	layout := legacySnapshotLayout{
		Sources: map[LegacySourceKind][]string{
			LegacySourceProject:    {"created_at", "id", "name", "type"},
			LegacySourceTask:       {"id", "priority", "project_id"},
			LegacySourceRule:       {"id", "task_id"},
			LegacySourceOccurrence: {"occurrence_date", "status", "task_id"},
			LegacySourceEvent:      {"end_time", "id", "is_all_day", "project_id", "start_time"},
			LegacySourceRoadmap:    {"id", "project_id"},
		},
		ProjectDeleted:        hasLegacySnapshotColumn(columns, "task_projects", "deleted_at"),
		TaskDeleted:           hasLegacySnapshotColumn(columns, "tasks", "deleted_at"),
		OccurrenceDeleted:     hasLegacySnapshotColumn(columns, "task_occurrences", "deleted_at"),
		EventDeleted:          hasLegacySnapshotColumn(columns, "events", "deleted_at"),
		TaskRoadmapNodeColumn: hasLegacySnapshotColumn(columns, "tasks", "roadmap_node_id"),
	}
	if dialect == DialectPostgres {
		layout.TaskDueColumn = "due_at"
		layout.EventStartColumn = "start_at"
		layout.EventEndColumn = "end_at"
	} else {
		layout.TaskDueColumn = "due"
		layout.EventStartColumn = "start_time"
		layout.EventEndColumn = "end_time"
	}
	layout.RoadmapNodePositionJSON = hasLegacySnapshotColumn(columns, "roadmap_nodes", "position")
	if layout.RoadmapNodePositionJSON {
	} else if !hasLegacySnapshotColumn(columns, "roadmap_nodes", "x") || !hasLegacySnapshotColumn(columns, "roadmap_nodes", "y") {
		missing := "x"
		if hasLegacySnapshotColumn(columns, "roadmap_nodes", "x") {
			missing = "y"
		}
		return legacySnapshotLayout{}, &LegacySnapshotSchemaError{Code: LegacySnapshotMissingColumn, Table: "roadmap_nodes", Column: missing}
	}
	if !hasLegacySnapshotColumn(columns, "tasks", layout.TaskDueColumn) {
		return legacySnapshotLayout{}, &LegacySnapshotSchemaError{Code: LegacySnapshotMissingColumn, Table: "tasks", Column: layout.TaskDueColumn}
	}
	if !hasLegacySnapshotColumn(columns, "events", layout.EventStartColumn) {
		return legacySnapshotLayout{}, &LegacySnapshotSchemaError{Code: LegacySnapshotMissingColumn, Table: "events", Column: layout.EventStartColumn}
	}
	if !hasLegacySnapshotColumn(columns, "events", layout.EventEndColumn) {
		return legacySnapshotLayout{}, &LegacySnapshotSchemaError{Code: LegacySnapshotMissingColumn, Table: "events", Column: layout.EventEndColumn}
	}
	return layout, nil
}

func hasLegacySnapshotColumn(columns map[string]map[string]struct{}, table, column string) bool {
	_, exists := columns[table][column]
	return exists
}

func (l *LegacySnapshotLoader) loadProjects(ctx context.Context, tx *sql.Tx, layout legacySnapshotLayout, inventory *LegacyTaskDomainInventory, destination *LegacyTaskDomainRows) error {
	query := `SELECT id,name,type,created_at FROM task_projects WHERE workspace_id=` + l.placeholder(1)
	if layout.ProjectDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY id`
	rows, err := tx.QueryContext(ctx, query, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, projectType string
		var createdValue any
		if err := rows.Scan(&id, &name, &projectType, &createdValue); err != nil {
			return fmt.Errorf("scan legacy project: %w", err)
		}
		createdAt, err := legacySnapshotTime(createdValue)
		if err != nil {
			return fmt.Errorf("scan legacy project %s created_at: %w", id, err)
		}
		project := LegacyProject{ID: id, Name: name, Type: LegacyProjectType(projectType), CreatedAt: createdAt}
		inventory.Projects = append(inventory.Projects, project)
		destination.Projects = append(destination.Projects, LegacyProjectRow{ID: id, Name: name, Type: project.Type})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy projects: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) loadRoadmaps(ctx context.Context, tx *sql.Tx, layout legacySnapshotLayout, destination *LegacyTaskDomainRows) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,project_id,title,goal,status,created_at,updated_at
		FROM learning_roadmaps WHERE workspace_id=`+l.placeholder(1)+` ORDER BY id`, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy learning roadmaps: %w", err)
	}
	for rows.Next() {
		var row LegacyRoadmapRow
		var createdValue, updatedValue any
		if err := rows.Scan(&row.ID, &row.ProjectID, &row.Title, &row.Goal, &row.Status, &createdValue, &updatedValue); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy learning roadmap: %w", err)
		}
		row.CreatedAt, err = legacySnapshotTime(createdValue)
		if err == nil {
			row.UpdatedAt, err = legacySnapshotTime(updatedValue)
		}
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy learning roadmap %s timestamps: %w", row.ID, err)
		}
		destination.Roadmaps = append(destination.Roadmaps, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate legacy learning roadmaps: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy learning roadmaps: %w", err)
	}

	positionColumns := `x,y`
	if layout.RoadmapNodePositionJSON {
		positionColumns = `position,NULL`
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,roadmap_id,COALESCE(parent_id,''),type,title,description,path_type,status,
		deliverable,acceptance_criteria,`+positionColumns+`,order_index,article_search_queries,created_at,updated_at
		FROM roadmap_nodes WHERE workspace_id=`+l.placeholder(1)+` ORDER BY roadmap_id,order_index,id`, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy roadmap nodes: %w", err)
	}
	for rows.Next() {
		var row LegacyRoadmapNodeRow
		var positionValue, secondPositionValue, queriesValue, createdValue, updatedValue any
		if err := rows.Scan(&row.ID, &row.RoadmapID, &row.ParentID, &row.Type, &row.Title, &row.Description, &row.PathType, &row.Status,
			&row.Deliverable, &row.AcceptanceCriteria, &positionValue, &secondPositionValue, &row.OrderIndex, &queriesValue, &createdValue, &updatedValue); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy roadmap node: %w", err)
		}
		if layout.RoadmapNodePositionJSON {
			row.CanvasX, row.CanvasY, err = legacySnapshotRoadmapPosition(positionValue)
		} else {
			row.CanvasX, err = legacySnapshotFloat(positionValue)
			if err == nil {
				row.CanvasY, err = legacySnapshotFloat(secondPositionValue)
			}
		}
		if err == nil {
			row.ArticleSearchQueries, err = legacySnapshotStringList(queriesValue)
		}
		if err == nil {
			row.CreatedAt, err = legacySnapshotTime(createdValue)
		}
		if err == nil {
			row.UpdatedAt, err = legacySnapshotTime(updatedValue)
		}
		if err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy roadmap node %s metadata: %w", row.ID, err)
		}
		destination.RoadmapNodes = append(destination.RoadmapNodes, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate legacy roadmap nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy roadmap nodes: %w", err)
	}

	rows, err = tx.QueryContext(ctx, `SELECT id,roadmap_id,source_node_id,target_node_id,style,created_at
		FROM roadmap_edges WHERE workspace_id=`+l.placeholder(1)+` ORDER BY roadmap_id,id`, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy roadmap edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LegacyRoadmapEdgeRow
		var createdValue any
		if err := rows.Scan(&row.ID, &row.RoadmapID, &row.SourceNodeID, &row.TargetNodeID, &row.Style, &createdValue); err != nil {
			return fmt.Errorf("scan legacy roadmap edge: %w", err)
		}
		row.CreatedAt, err = legacySnapshotTime(createdValue)
		if err != nil {
			return fmt.Errorf("scan legacy roadmap edge %s created_at: %w", row.ID, err)
		}
		destination.RoadmapEdges = append(destination.RoadmapEdges, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy roadmap edges: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) loadTasks(ctx context.Context, tx *sql.Tx, layout legacySnapshotLayout, inventory *LegacyTaskDomainInventory, destination *LegacyTaskDomainRows) error {
	roadmapNodeExpression := `''`
	if layout.TaskRoadmapNodeColumn {
		roadmapNodeExpression = `COALESCE(roadmap_node_id,'')`
	}
	query := `SELECT id,COALESCE(project_id,''),` + roadmapNodeExpression + `,execution_type,title,content,priority,sort_order,planned_date,` + quoteLegacySnapshotIdentifier(layout.TaskDueColumn) + `,status,done,completed_at,updated_at,COALESCE(note_id,'') FROM tasks WHERE workspace_id=` + l.placeholder(1)
	if layout.TaskDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY id`
	rows, err := tx.QueryContext(ctx, query, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LegacyTaskRow
		var executionType, status string
		var plannedValue, dueValue, completedValue, updatedValue any
		var doneValue any
		if err := rows.Scan(&row.ID, &row.ProjectID, &row.RoadmapNodeID, &executionType, &row.Title, &row.Content, &row.Priority, &row.SortOrder, &plannedValue, &dueValue, &status, &doneValue, &completedValue, &updatedValue, &row.NoteID); err != nil {
			return fmt.Errorf("scan legacy task: %w", err)
		}
		row.ExecutionType = LegacyExecutionType(executionType)
		row.Status = taskdomain.ExecutionStatus(status)
		row.PlannedDate = legacySnapshotDate(plannedValue)
		row.Done, err = legacySnapshotBool(doneValue)
		if err != nil {
			return fmt.Errorf("scan legacy task %s done: %w", row.ID, err)
		}
		row.DueAt, err = legacySnapshotOptionalTime(dueValue)
		if err != nil {
			return fmt.Errorf("scan legacy task %s due: %w", row.ID, err)
		}
		row.CompletedAt, err = legacySnapshotOptionalTime(completedValue)
		if err != nil {
			return fmt.Errorf("scan legacy task %s completed_at: %w", row.ID, err)
		}
		row.UpdatedAt, err = legacySnapshotTime(updatedValue)
		if err != nil {
			return fmt.Errorf("scan legacy task %s updated_at: %w", row.ID, err)
		}
		inventory.Tasks = append(inventory.Tasks, LegacyTask{ID: row.ID, ProjectID: row.ProjectID, Priority: row.Priority})
		destination.Tasks = append(destination.Tasks, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy tasks: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) loadRules(ctx context.Context, tx *sql.Tx, _ legacySnapshotLayout, destination *LegacyTaskDomainRows) error {
	query := `SELECT task_id,start_date,end_date,frequency,interval,weekdays,month_days,timezone FROM task_recurrence_rules WHERE workspace_id=` + l.placeholder(1) + ` ORDER BY task_id`
	rows, err := tx.QueryContext(ctx, query, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy recurrence rules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LegacyRuleRow
		var startValue, endValue, weekdaysValue, monthDaysValue any
		var frequency string
		if err := rows.Scan(&row.TaskID, &startValue, &endValue, &frequency, &row.Interval, &weekdaysValue, &monthDaysValue, &row.Timezone); err != nil {
			return fmt.Errorf("scan legacy recurrence rule: %w", err)
		}
		row.ID = row.TaskID
		row.StartsOn = legacySnapshotDate(startValue)
		row.EndsOn = legacySnapshotDate(endValue)
		row.RecurrenceType = taskdomain.RecurrenceType(frequency)
		row.TimingType = taskdomain.TimingDate
		row.Weekdays, err = legacySnapshotIntList(weekdaysValue)
		if err != nil {
			return fmt.Errorf("scan legacy recurrence rule %s weekdays: %w", row.TaskID, err)
		}
		row.MonthDays, err = legacySnapshotIntList(monthDaysValue)
		if err != nil {
			return fmt.Errorf("scan legacy recurrence rule %s month_days: %w", row.TaskID, err)
		}
		destination.Rules = append(destination.Rules, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy recurrence rules: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) loadOccurrences(ctx context.Context, tx *sql.Tx, layout legacySnapshotLayout, destination *LegacyTaskDomainRows) error {
	query := `SELECT task_id,occurrence_date,status,completed_at,updated_at,note FROM task_occurrences WHERE workspace_id=` + l.placeholder(1)
	if layout.OccurrenceDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY task_id,occurrence_date`
	rows, err := tx.QueryContext(ctx, query, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy occurrences: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LegacyOccurrenceRow
		var dateValue, completedValue, updatedValue any
		var status, note string
		if err := rows.Scan(&row.TaskID, &dateValue, &status, &completedValue, &updatedValue, &note); err != nil {
			return fmt.Errorf("scan legacy occurrence: %w", err)
		}
		row.OccurrenceDate = legacySnapshotDate(dateValue)
		row.ID = legacySnapshotOccurrenceIdentity(l.config.Dialect, row.TaskID, row.OccurrenceDate)
		row.Status = taskdomain.ExecutionStatus(status)
		row.CompletedAt, err = legacySnapshotOptionalTime(completedValue)
		if err != nil {
			return fmt.Errorf("scan legacy occurrence %s completed_at: %w", row.ID, err)
		}
		row.UpdatedAt, err = legacySnapshotTime(updatedValue)
		if err != nil {
			return fmt.Errorf("scan legacy occurrence %s updated_at: %w", row.ID, err)
		}
		row.Note = note
		destination.Occurrences = append(destination.Occurrences, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy occurrences: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) loadEvents(ctx context.Context, tx *sql.Tx, layout legacySnapshotLayout, inventory *LegacyTaskDomainInventory, destination *LegacyTaskDomainRows) error {
	query := `SELECT id,COALESCE(project_id,''),title,` + quoteLegacySnapshotIdentifier(layout.EventStartColumn) + `,` + quoteLegacySnapshotIdentifier(layout.EventEndColumn) + `,is_all_day,COALESCE(location,''),kind,notes,COALESCE(note_id,'') FROM events WHERE workspace_id=` + l.placeholder(1)
	if layout.EventDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY id`
	rows, err := tx.QueryContext(ctx, query, l.config.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load legacy events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row LegacyEventRow
		var startValue, endValue, allDayValue any
		if err := rows.Scan(&row.ID, &row.ProjectID, &row.Title, &startValue, &endValue, &allDayValue, &row.Location, &row.Kind, &row.Notes, &row.NoteID); err != nil {
			return fmt.Errorf("scan legacy event: %w", err)
		}
		row.StartAt, err = legacySnapshotTime(startValue)
		if err != nil {
			return fmt.Errorf("scan legacy event %s start: %w", row.ID, err)
		}
		row.EndAt, err = legacySnapshotTime(endValue)
		if err != nil {
			return fmt.Errorf("scan legacy event %s end: %w", row.ID, err)
		}
		row.AllDay, err = legacySnapshotBool(allDayValue)
		if err != nil {
			return fmt.Errorf("scan legacy event %s all-day: %w", row.ID, err)
		}
		inventory.Events = append(inventory.Events, LegacyEvent{ID: row.ID, AllDay: row.AllDay, StartAt: row.StartAt, EndAt: row.EndAt})
		destination.Events = append(destination.Events, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy events: %w", err)
	}
	return nil
}

func (l *LegacySnapshotLoader) placeholder(index int) string {
	if l.config.Dialect == DialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func quoteLegacySnapshotIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func cloneLegacySnapshotSources(source map[LegacySourceKind][]string) map[LegacySourceKind][]string {
	result := make(map[LegacySourceKind][]string, len(source))
	for kind, columns := range source {
		result[kind] = append([]string(nil), columns...)
		sort.Strings(result[kind])
	}
	return result
}

func legacySnapshotTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed, nil
	case int64:
		return time.Unix(typed, 0).UTC(), nil
	case int:
		return time.Unix(int64(typed), 0).UTC(), nil
	case float64:
		return time.Unix(int64(typed), 0).UTC(), nil
	case []byte:
		return legacySnapshotTime(string(typed))
	case string:
		value := strings.TrimSpace(typed)
		if value == "" {
			return time.Time{}, fmt.Errorf("empty timestamp")
		}
		if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.Unix(unix, 0).UTC(), nil
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
	default:
		return time.Time{}, fmt.Errorf("unsupported timestamp type %T", value)
	}
}

func legacySnapshotOptionalTime(value any) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return nil, nil
	}
	parsed, err := legacySnapshotTime(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func legacySnapshotDate(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case time.Time:
		return typed.Format("2006-01-02")
	case []byte:
		return legacySnapshotDate(string(typed))
	case string:
		value := strings.TrimSpace(typed)
		if len(value) >= len("2006-01-02") {
			return value[:len("2006-01-02")]
		}
		return value
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func legacySnapshotBool(value any) (bool, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case int64:
		if typed == 0 {
			return false, nil
		}
		if typed == 1 {
			return true, nil
		}
	case int:
		if typed == 0 {
			return false, nil
		}
		if typed == 1 {
			return true, nil
		}
	case []byte:
		return legacySnapshotBool(string(typed))
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "0", "false":
			return false, nil
		case "1", "true":
			return true, nil
		}
	}
	return false, fmt.Errorf("unsupported boolean %v (%T)", value, value)
}

func legacySnapshotFloat(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case []byte:
		return strconv.ParseFloat(string(typed), 64)
	case string:
		return strconv.ParseFloat(typed, 64)
	default:
		return 0, fmt.Errorf("unsupported float value %T", value)
	}
}

func legacySnapshotRoadmapPosition(value any) (float64, float64, error) {
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return 0, 0, fmt.Errorf("unsupported roadmap position %T", value)
	}
	var position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := json.Unmarshal(raw, &position); err != nil {
		return 0, 0, err
	}
	return position.X, position.Y, nil
}

func legacySnapshotStringList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, fmt.Sprint(item))
		}
		return result, nil
	case []byte:
		return legacySnapshotStringList(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" || trimmed == "[]" || trimmed == "{}" {
			return nil, nil
		}
		var result []string
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
				return nil, err
			}
			return result, nil
		}
		if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
			body := strings.TrimSuffix(strings.TrimPrefix(trimmed, "{"), "}")
			if strings.TrimSpace(body) == "" {
				return nil, nil
			}
			for _, item := range strings.Split(body, ",") {
				result = append(result, strings.Trim(strings.TrimSpace(item), `"`))
			}
			return result, nil
		}
		return nil, fmt.Errorf("unsupported string-list encoding %q", typed)
	default:
		return nil, fmt.Errorf("unsupported string-list value %T", value)
	}
}

func legacySnapshotIntList(value any) ([]int, error) {
	if value == nil {
		return nil, nil
	}
	var text string
	switch typed := value.(type) {
	case string:
		text = strings.TrimSpace(typed)
	case []byte:
		text = strings.TrimSpace(string(typed))
	case []int32:
		result := make([]int, len(typed))
		for index := range typed {
			result[index] = int(typed[index])
		}
		sort.Ints(result)
		return result, nil
	case []int64:
		result := make([]int, len(typed))
		for index := range typed {
			result[index] = int(typed[index])
		}
		sort.Ints(result)
		return result, nil
	default:
		text = strings.TrimSpace(fmt.Sprint(value))
	}
	if text == "" || text == "[]" || text == "{}" {
		return nil, nil
	}
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		text = "[" + strings.TrimSuffix(strings.TrimPrefix(text, "{"), "}") + "]"
	}
	var values []int
	if err := json.Unmarshal([]byte(text), &values); err != nil {
		return nil, fmt.Errorf("decode integer list %q: %w", text, err)
	}
	sort.Ints(values)
	return values, nil
}

func legacySnapshotOccurrenceIdentity(dialect Dialect, taskID, occurrenceDate string) string {
	if dialect == DialectPostgres {
		left, _ := json.Marshal(taskID)
		right, _ := json.Marshal(occurrenceDate)
		return "[" + string(left) + ", " + string(right) + "]"
	}
	encoded, _ := json.Marshal([]string{taskID, occurrenceDate})
	return string(encoded)
}
