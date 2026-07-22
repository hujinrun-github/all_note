package taskmigration

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	sqlitestore "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	_ "modernc.org/sqlite"
)

func TestLegacySnapshotLoaderReadsRealSQLiteSchemaWithoutCrossWorkspaceData(t *testing.T) {
	db := openLegacySnapshotSQLite(t)
	seedLegacySnapshotSQLite(t, db)

	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{
		Dialect:            DialectSQLite,
		WorkspaceID:        "alpha",
		WorkspaceTimezone:  "Asia/Shanghai",
		OwnerTimezone:      "Asia/Tokyo",
		DeploymentTimezone: "UTC",
	})
	if err != nil {
		t.Fatalf("NewLegacySnapshotLoader: %v", err)
	}

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	defer tx.Rollback()
	inventory, rows, err := loader.Load(context.Background(), tx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if inventory.WorkspaceID != "alpha" || inventory.WorkspaceTimezone != "Asia/Shanghai" || inventory.OwnerTimezone != "Asia/Tokyo" || inventory.DeploymentTimezone != "UTC" {
		t.Fatalf("inventory identity/timezones = %#v", inventory)
	}
	if got := inventory.Sources[LegacySourceEvent]; !reflect.DeepEqual(got, []string{"end_time", "id", "is_all_day", "project_id", "start_time"}) {
		t.Fatalf("canonical event source columns = %#v", got)
	}
	if got := idsFromLegacyProjects(inventory.Projects); !reflect.DeepEqual(got, []string{"a-project", "z-project"}) {
		t.Fatalf("inventory project IDs = %#v", got)
	}
	if got := idsFromLegacyTasks(inventory.Tasks); !reflect.DeepEqual(got, []string{"a-task", "z-task"}) {
		t.Fatalf("inventory task IDs = %#v", got)
	}
	if got := idsFromLegacyEvents(inventory.Events); !reflect.DeepEqual(got, []string{"all-day-event", "timed-event"}) {
		t.Fatalf("inventory event IDs = %#v", got)
	}

	if got := projectRowIDs(rows.Projects); !reflect.DeepEqual(got, []string{"a-project", "z-project"}) {
		t.Fatalf("project row IDs = %#v", got)
	}
	if got := taskRowIDs(rows.Tasks); !reflect.DeepEqual(got, []string{"a-task", "z-task"}) {
		t.Fatalf("task row IDs = %#v", got)
	}
	if got := ruleRowIDs(rows.Rules); !reflect.DeepEqual(got, []string{"z-task"}) {
		t.Fatalf("rule row IDs = %#v", got)
	}
	if got := occurrenceRowIDs(rows.Occurrences); !reflect.DeepEqual(got, []string{`["z-task","2026-07-07"]`}) {
		t.Fatalf("occurrence row IDs = %#v", got)
	}
	if got := eventRowIDs(rows.Events); !reflect.DeepEqual(got, []string{"all-day-event", "timed-event"}) {
		t.Fatalf("event row IDs = %#v", got)
	}
	if got := roadmapRowIDs(rows.Roadmaps); !reflect.DeepEqual(got, []string{"alpha-roadmap"}) {
		t.Fatalf("roadmap row IDs = %#v", got)
	}
	if len(rows.RoadmapNodes) != 2 || rows.RoadmapNodes[0].ID != "alpha-root" || rows.RoadmapNodes[1].ID != "alpha-child" || rows.RoadmapNodes[1].ParentID != "alpha-root" || rows.RoadmapNodes[1].Deliverable != "service" {
		t.Fatalf("roadmap nodes = %#v", rows.RoadmapNodes)
	}
	if len(rows.RoadmapEdges) != 1 || rows.RoadmapEdges[0].SourceNodeID != "alpha-root" || rows.RoadmapEdges[0].TargetNodeID != "alpha-child" {
		t.Fatalf("roadmap edges = %#v", rows.RoadmapEdges)
	}

	single := rows.Tasks[0]
	if single.ExecutionType != LegacyExecutionSingle || single.RoadmapNodeID != "alpha-child" || single.Content != "single body" || single.PlannedDate != "2026-07-03" || single.DueAt == nil || single.CompletedAt == nil || single.NoteID != "alpha-note" || single.Status != taskdomain.ExecutionStatusDone || !single.Done {
		t.Fatalf("single task fields not preserved: %#v", single)
	}
	rule := rows.Rules[0]
	if rule.TaskID != "z-task" || rule.RecurrenceType != taskdomain.RecurrenceWeekly || rule.TimingType != taskdomain.TimingDate || rule.Timezone != "Asia/Shanghai" || rule.StartsOn != "2026-07-01" || rule.EndsOn != "2026-08-01" || rule.Interval != 2 || !reflect.DeepEqual(rule.Weekdays, []int{1, 3}) || !reflect.DeepEqual(rule.MonthDays, []int{2, 15}) {
		t.Fatalf("recurrence rule not preserved: %#v", rule)
	}
	occurrence := rows.Occurrences[0]
	if occurrence.TaskID != "z-task" || occurrence.OccurrenceDate != "2026-07-07" || occurrence.Status != taskdomain.ExecutionStatusSkipped || occurrence.BlockedReason != "" || occurrence.NextAction != "" {
		t.Fatalf("occurrence state/note not preserved: %#v", occurrence)
	}
	allDay := rows.Events[0]
	if !allDay.AllDay || allDay.Location != "上海" || allDay.Kind != "focus" || allDay.Notes != "日历备注" || allDay.NoteID != "event-note" {
		t.Fatalf("all-day metadata not preserved: %#v", allDay)
	}
	location, _ := time.LoadLocation("Asia/Shanghai")
	if got := allDay.StartAt.In(location).Format(time.RFC3339); got != "2026-07-01T00:00:00+08:00" {
		t.Fatalf("all-day start = %s", got)
	}
	if got := allDay.EndAt.In(location).Format(time.RFC3339); got != "2026-07-04T00:00:00+08:00" {
		t.Fatalf("all-day end = %s", got)
	}
}

func TestLegacySnapshotLoaderFailsClosedOnMissingTableOrColumn(t *testing.T) {
	tests := []struct {
		name      string
		mutate    string
		wantCode  LegacySnapshotSchemaErrorCode
		wantTable string
		wantCol   string
	}{
		{name: "missing table", mutate: `DROP TABLE events`, wantCode: LegacySnapshotMissingTable, wantTable: "events"},
		{name: "missing column", mutate: `ALTER TABLE events RENAME TO events_complete;
			CREATE TABLE events(id TEXT, workspace_id TEXT, title TEXT, start_time INTEGER, is_all_day INTEGER, project_id TEXT, location TEXT, kind TEXT, notes TEXT, note_id TEXT)`, wantCode: LegacySnapshotMissingColumn, wantTable: "events", wantCol: "end_time"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openLegacySnapshotSQLite(t)
			if _, err := db.Exec(test.mutate); err != nil {
				t.Fatalf("mutate schema: %v", err)
			}
			loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{Dialect: DialectSQLite, WorkspaceID: "alpha"})
			if err != nil {
				t.Fatalf("NewLegacySnapshotLoader: %v", err)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback()
			_, _, err = loader.Load(context.Background(), tx)
			var schemaErr *LegacySnapshotSchemaError
			if !errors.As(err, &schemaErr) || schemaErr.Code != test.wantCode || schemaErr.Table != test.wantTable || schemaErr.Column != test.wantCol {
				t.Fatalf("Load error = %#v, want code=%s table=%s column=%s", err, test.wantCode, test.wantTable, test.wantCol)
			}
		})
	}
}

func TestLegacySnapshotLoaderPreservesBlockedOccurrenceNoteWhenHistoricalSchemaAllowsIt(t *testing.T) {
	db := openLegacySnapshotSQLite(t)
	seedLegacySnapshotSQLite(t, db)
	if _, err := db.Exec(`DROP TABLE task_occurrences;
		CREATE TABLE task_occurrences(
		 task_id TEXT NOT NULL, workspace_id TEXT NOT NULL, occurrence_date TEXT NOT NULL,
		 status TEXT NOT NULL, completed_at INTEGER, note TEXT NOT NULL DEFAULT '',
		 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, occurrence_id TEXT, revision INTEGER NOT NULL DEFAULT 1, deleted_at INTEGER,
		 PRIMARY KEY(task_id,occurrence_date));
		INSERT INTO task_occurrences(task_id,workspace_id,occurrence_date,status,note,created_at,updated_at,occurrence_id)
		VALUES('z-task','alpha','2026-07-08','blocked','等待评审',100,200,'ignored-mobile-id')`); err != nil {
		t.Fatalf("install historical blocked occurrence fixture: %v", err)
	}
	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{Dialect: DialectSQLite, WorkspaceID: "alpha"})
	if err != nil {
		t.Fatalf("NewLegacySnapshotLoader: %v", err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	_, rows, err := loader.Load(context.Background(), tx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rows.Occurrences) != 1 || rows.Occurrences[0].Status != taskdomain.ExecutionStatusBlocked || rows.Occurrences[0].Note != "等待评审" || rows.Occurrences[0].BlockedReason != "" {
		t.Fatalf("blocked occurrence = %#v", rows.Occurrences)
	}
}

func TestBuildLegacySnapshotLayoutAdaptsHistoricalEventColumnsExplicitly(t *testing.T) {
	tables := completeLegacySnapshotColumns()
	tables["events"] = []string{"id", "workspace_id", "title", "start_at", "end_at", "is_all_day", "project_id", "location", "kind", "notes", "note_id", "deleted_at"}
	layout, err := buildLegacySnapshotLayout(DialectPostgres, tables)
	if err != nil {
		t.Fatalf("buildLegacySnapshotLayout: %v", err)
	}
	if layout.EventStartColumn != "start_at" || layout.EventEndColumn != "end_at" || layout.TaskDueColumn != "due_at" {
		t.Fatalf("postgres historical layout = %#v", layout)
	}
	if got := layout.Sources[LegacySourceEvent]; !reflect.DeepEqual(got, []string{"end_time", "id", "is_all_day", "project_id", "start_time"}) {
		t.Fatalf("canonical event source = %#v", got)
	}

	tables["events"] = []string{"id", "workspace_id", "title", "start_time", "end_time", "is_all_day", "project_id", "location", "kind", "notes", "note_id", "deleted_at"}
	tables["tasks"] = replaceColumn(tables["tasks"], "due_at", "due")
	layout, err = buildLegacySnapshotLayout(DialectSQLite, tables)
	if err != nil {
		t.Fatalf("build sqlite layout: %v", err)
	}
	if layout.EventStartColumn != "start_time" || layout.EventEndColumn != "end_time" || layout.TaskDueColumn != "due" {
		t.Fatalf("sqlite layout = %#v", layout)
	}
}

func TestNewLegacySnapshotLoaderValidatesTransactionScope(t *testing.T) {
	if _, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{Dialect: Dialect("oracle"), WorkspaceID: "alpha"}); !errors.Is(err, ErrInvalidLegacySnapshotLoader) {
		t.Fatalf("unsupported dialect error = %v", err)
	}
	if _, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{Dialect: DialectSQLite}); !errors.Is(err, ErrInvalidLegacySnapshotLoader) {
		t.Fatalf("missing workspace error = %v", err)
	}
	loader, err := NewLegacySnapshotLoader(LegacySnapshotLoaderConfig{Dialect: DialectSQLite, WorkspaceID: "alpha"})
	if err != nil {
		t.Fatalf("NewLegacySnapshotLoader: %v", err)
	}
	if _, _, err := loader.Load(context.Background(), nil); !errors.Is(err, ErrInvalidLegacySnapshotLoader) {
		t.Fatalf("nil transaction error = %v", err)
	}
}

func openLegacySnapshotSQLite(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-snapshot.db")
	opened, err := (sqlitestore.Provider{}).Open(context.Background(), storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path})
	if err != nil {
		t.Fatalf("open real SQLite provider: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatalf("close provider: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedLegacySnapshotSQLite(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO users(id,email,display_name,password_hash,created_at,updated_at) VALUES
		 ('alpha-owner','alpha@example.test','Alpha','x',1,1),('beta-owner','beta@example.test','Beta','x',1,1)`,
		`INSERT INTO workspaces(id,name,owner_user_id,created_at,updated_at) VALUES
		 ('alpha','Alpha','alpha-owner',1,1),('beta','Beta','beta-owner',1,1)`,
		`INSERT INTO workspace_members(workspace_id,user_id,role,created_at) VALUES
		 ('alpha','alpha-owner','owner',1),('beta','beta-owner','owner',1)`,
		`INSERT INTO folders(id,workspace_id,name,sort_order,created_at) VALUES
		 ('__uncategorized','alpha','Alpha uncategorized',0,1),
		 ('__uncategorized','beta','Beta uncategorized',0,1)`,
		`INSERT INTO task_projects(id,workspace_id,name,type,description,created_at,updated_at) VALUES
		 ('z-project','alpha','Zulu','personal','',200,200),
		 ('a-project','alpha','Alpha','learning','',100,100),
		 ('beta-project','beta','Beta','regular','',50,50)`,
		`INSERT INTO learning_roadmaps(id,workspace_id,project_id,title,goal,status,created_at,updated_at) VALUES
		 ('alpha-roadmap','alpha','a-project','Alpha roadmap','Ship','active',100,200),
		 ('beta-roadmap','beta','beta-project','Beta roadmap','Secret','active',100,200)`,
		`INSERT INTO roadmap_nodes(id,workspace_id,roadmap_id,parent_id,type,title,description,path_type,status,deliverable,acceptance_criteria,x,y,order_index,article_search_queries,created_at,updated_at) VALUES
		 ('alpha-root','alpha','alpha-roadmap',NULL,'phase','Root','Basics','required','done','notes','quiz',10,20,10,'["Go tour"]',100,200),
		 ('alpha-child','alpha','alpha-roadmap','alpha-root','task','Child','API','recommended','active','service','tests',30,40,20,'["Go HTTP"]',100,200),
		 ('beta-node','beta','beta-roadmap',NULL,'task','Secret','Secret','required','todo','','',0,0,0,'[]',100,200)`,
		`INSERT INTO roadmap_edges(id,workspace_id,roadmap_id,source_node_id,target_node_id,style,created_at) VALUES
		 ('alpha-edge','alpha','alpha-roadmap','alpha-root','alpha-child','solid',100)`,
		`INSERT INTO notes(id,workspace_id,title,body,folder_id,tags,created_at,updated_at) VALUES
		 ('alpha-note','alpha','Task note','','__uncategorized','[]',1,1),
		 ('event-note','alpha','Event note','','__uncategorized','[]',1,1)`,
		`INSERT INTO tasks(id,workspace_id,title,content,project_id,roadmap_node_id,due,planned_date,priority,done,status,horizon,scope,sort_order,note_id,execution_type,created_at,updated_at,completed_at) VALUES
		 ('z-task','alpha','Recurring','repeat body','z-project',NULL,NULL,NULL,1,0,'active','week','daily',20,NULL,'recurring',100,200,NULL),
		 ('a-task','alpha','Done once','single body','a-project','alpha-child',1783040400,'2026-07-03',2,1,'done','week','daily',10,'alpha-note','single',100,1783037045,1783037045),
		 ('beta-task','beta','Other tenant','secret','beta-project','beta-node',NULL,NULL,0,0,'open','week','daily',0,NULL,'single',100,100,NULL)`,
		`INSERT INTO task_recurrence_rules(task_id,workspace_id,start_date,end_date,frequency,interval,weekdays,month_days,timezone,enabled,created_at,updated_at) VALUES
		 ('z-task','alpha','2026-07-01','2026-08-01','weekly',2,'[3,1]','[15,2]','Asia/Shanghai',1,100,200),
		 ('beta-task','beta','2026-07-01',NULL,'daily',1,'[]','[]','UTC',1,100,200)`,
		`INSERT INTO task_occurrences(task_id,workspace_id,occurrence_date,status,completed_at,note,created_at,updated_at,occurrence_id) VALUES
		 ('z-task','alpha','2026-07-07','skipped',NULL,'跳过原因',100,200,'alpha-occurrence'),
		 ('beta-task','beta','2026-07-07','open',NULL,'secret',100,200,'beta-occurrence')`,
		`INSERT INTO events(id,workspace_id,title,start_time,end_time,location,kind,is_all_day,notes,note_id,project_id,created_at,updated_at) VALUES
		 ('timed-event','alpha','Timed',1782896400,1782900000,'线上','meeting',0,'timed notes',NULL,'a-project',100,200),
		 ('all-day-event','alpha','All day',1782835200,1783094400,'上海','focus',1,'日历备注','event-note','z-project',100,200),
		 ('beta-event','beta','Secret',1782896400,1782900000,'','work',0,'',NULL,'beta-project',100,200)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed real SQLite legacy schema: %v\n%s", err, statement)
		}
	}
}

func completeLegacySnapshotColumns() map[string][]string {
	return map[string][]string{
		"task_projects":         {"id", "workspace_id", "name", "type", "created_at", "deleted_at"},
		"tasks":                 {"id", "workspace_id", "project_id", "execution_type", "title", "content", "priority", "sort_order", "planned_date", "due_at", "status", "done", "completed_at", "updated_at", "note_id", "deleted_at"},
		"task_recurrence_rules": {"task_id", "workspace_id", "start_date", "end_date", "frequency", "interval", "weekdays", "month_days", "timezone", "enabled", "updated_at"},
		"task_occurrences":      {"task_id", "workspace_id", "occurrence_date", "occurrence_id", "status", "completed_at", "updated_at", "note", "deleted_at"},
		"events":                {"id", "workspace_id", "title", "start_time", "end_time", "is_all_day", "project_id", "location", "kind", "notes", "note_id", "deleted_at"},
		"roadmap_nodes":         {"id", "workspace_id", "roadmap_id", "parent_id", "type", "title", "description", "path_type", "status", "deliverable", "acceptance_criteria", "position", "order_index", "article_search_queries", "created_at", "updated_at"},
		"roadmap_edges":         {"id", "workspace_id", "roadmap_id", "source_node_id", "target_node_id", "style", "created_at"},
		"learning_roadmaps":     {"id", "workspace_id", "project_id", "title", "goal", "status", "created_at", "updated_at"},
	}
}

func replaceColumn(columns []string, old, replacement string) []string {
	result := append([]string(nil), columns...)
	for index := range result {
		if result[index] == old {
			result[index] = replacement
		}
	}
	return result
}

func idsFromLegacyProjects(rows []LegacyProject) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func idsFromLegacyTasks(rows []LegacyTask) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func idsFromLegacyEvents(rows []LegacyEvent) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func projectRowIDs(rows []LegacyProjectRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func taskRowIDs(rows []LegacyTaskRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func ruleRowIDs(rows []LegacyRuleRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func occurrenceRowIDs(rows []LegacyOccurrenceRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func eventRowIDs(rows []LegacyEventRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
func roadmapRowIDs(rows []LegacyRoadmapRow) []string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i].ID
	}
	return result
}
