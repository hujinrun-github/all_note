package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func (r *sqliteTaskDomainV2ProjectReader) GetRoadmapByProject(ctx context.Context, projectID string) (taskdomain.RoadmapSnapshot, error) {
	return getSQLiteRoadmap(ctx, r.queryer, r.workspaceID, "project_id", projectID)
}
func (r *sqliteTaskDomainV2ProjectReader) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getSQLiteRoadmap(ctx, r.queryer, r.workspaceID, "id", id)
}
func getSQLiteRoadmap(ctx context.Context, q sqliteTaskDomainV2Queryer, workspaceID, column, value string) (taskdomain.RoadmapSnapshot, error) {
	var out taskdomain.RoadmapSnapshot
	var status string
	err := q.QueryRowContext(ctx, `SELECT workspace_id,id,project_id,status,title,description,revision FROM domain_learning_roadmaps_v2 WHERE workspace_id=? AND `+column+`=?`, workspaceID, value).Scan(&out.Roadmap.WorkspaceID, &out.Roadmap.ID, &out.Roadmap.ProjectID, &status, &out.Roadmap.Title, &out.Roadmap.Description, &out.Roadmap.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, taskdomain.ErrRoadmapNotFound
	}
	if err != nil {
		return out, err
	}
	out.Roadmap.Status = taskdomain.RoadmapStatus(status)
	rows, err := q.QueryContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,position,revision FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND roadmap_id=? ORDER BY position,id`, workspaceID, out.Roadmap.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanSQLiteRoadmapV2Node(rows)
		if err != nil {
			return out, err
		}
		p, err := sqliteRoadmapProgress(ctx, q, workspaceID, n.Node.ID)
		if err != nil {
			return out, err
		}
		n.Progress = p
		out.Nodes = append(out.Nodes, n)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	edges, err := q.QueryContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,from_node_id,to_node_id,edge_type,revision FROM domain_roadmap_edges_v2 WHERE workspace_id=? AND roadmap_id=? ORDER BY id`, workspaceID, out.Roadmap.ID)
	if err != nil {
		return out, err
	}
	defer edges.Close()
	for edges.Next() {
		var e taskdomain.RoadmapEdge
		var typ string
		if err = edges.Scan(&e.WorkspaceID, &e.ID, &e.ProjectID, &e.RoadmapID, &e.FromNodeID, &e.ToNodeID, &typ, &e.Revision); err != nil {
			return out, err
		}
		e.Type = taskdomain.RoadmapEdgeType(typ)
		out.Edges = append(out.Edges, e)
	}
	return out, edges.Err()
}
func (r *sqliteTaskDomainV2ProjectReader) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return getSQLiteRoadmapNode(ctx, r.queryer, r.workspaceID, id)
}
func (w *sqliteTaskDomainV2ProjectWriter) GetRoadmapByProject(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getSQLiteRoadmap(ctx, w.queryer, w.workspaceID, "project_id", id)
}
func (w *sqliteTaskDomainV2ProjectWriter) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getSQLiteRoadmap(ctx, w.queryer, w.workspaceID, "id", id)
}
func (w *sqliteTaskDomainV2ProjectWriter) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return getSQLiteRoadmapNode(ctx, w.queryer, w.workspaceID, id)
}
func getSQLiteRoadmapNode(ctx context.Context, q sqliteTaskDomainV2Queryer, w, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	row := q.QueryRowContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,position,revision FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=?`, w, id)
	n, err := scanSQLiteRoadmapV2Node(row)
	if errors.Is(err, sql.ErrNoRows) {
		return n, taskdomain.ErrRoadmapNodeNotFound
	}
	if err != nil {
		return n, err
	}
	n.Progress, err = sqliteRoadmapProgress(ctx, q, w, id)
	return n, err
}

type sqliteRoadmapScanner interface{ Scan(...any) error }

func scanSQLiteRoadmapV2Node(s sqliteRoadmapScanner) (taskdomain.RoadmapNodeSnapshot, error) {
	var n taskdomain.RoadmapNodeSnapshot
	var parent sql.NullString
	var typ string
	err := s.Scan(&n.Node.WorkspaceID, &n.Node.ID, &n.Node.ProjectID, &n.Node.RoadmapID, &parent, &n.Node.Title, &n.Node.Description, &typ, &n.Node.Position, &n.Node.Revision)
	n.Node.ParentID = parent.String
	n.Node.Type = taskdomain.RoadmapNodeType(typ)
	return n, err
}
func sqliteRoadmapProgress(ctx context.Context, q sqliteTaskDomainV2Queryer, w, node string) (taskdomain.RoadmapNodeProgress, error) {
	var p taskdomain.RoadmapNodeProgress
	err := q.QueryRowContext(ctx, `SELECT COUNT(DISTINCT t.id),COUNT(o.id),COALESCE(SUM(CASE WHEN o.execution_status='open' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='active' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='blocked' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='done' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='skipped' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='cancelled' THEN 1 ELSE 0 END),0) FROM domain_tasks_v2 t LEFT JOIN domain_task_occurrences_v2 o ON o.workspace_id=t.workspace_id AND o.task_id=t.id WHERE t.workspace_id=? AND t.roadmap_node_id=?`, w, node).Scan(&p.Tasks, &p.Total, &p.Open, &p.Active, &p.Blocked, &p.Done, &p.Skipped, &p.Cancelled)
	return p, err
}
func (w *sqliteTaskDomainV2ProjectWriter) CountRoadmapNodeTasks(ctx context.Context, id string) (int, error) {
	if w.isClosed != nil && w.isClosed() {
		return 0, storage.ErrTenantWriteTxClosed
	}
	var n int
	err := w.queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_tasks_v2 WHERE workspace_id=? AND roadmap_node_id=?`, w.workspaceID, id).Scan(&n)
	return n, err
}
func (w *sqliteTaskDomainV2ProjectWriter) CreateRoadmap(ctx context.Context, r taskdomain.LearningRoadmap) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	_, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,description,revision,created_at,updated_at)VALUES(?,?,?,?,?,?,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w.workspaceID, r.ID, r.ProjectID, r.Status, r.Title, r.Description)
	return err
}
func (w *sqliteTaskDomainV2ProjectWriter) CreateRoadmapNode(ctx context.Context, n taskdomain.RoadmapNode) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	_, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_roadmap_nodes_v2(workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,status,position,revision,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?, 'available',?,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, w.workspaceID, n.ID, n.ProjectID, n.RoadmapID, nullableSQLiteTaskDomainV2String(n.ParentID), n.Title, n.Description, n.Type, n.Position)
	return err
}
func (w *sqliteTaskDomainV2ProjectWriter) SaveRoadmapNode(ctx context.Context, x taskdomain.RoadmapNodeWrite) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	r, err := w.queryer.ExecContext(ctx, `UPDATE domain_roadmap_nodes_v2 SET parent_id=?,title=?,description=?,node_type=?,position=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE workspace_id=? AND id=? AND revision=?`, nullableSQLiteTaskDomainV2String(x.Node.ParentID), x.Node.Title, x.Node.Description, x.Node.Type, x.Node.Position, w.workspaceID, x.Node.ID, x.ExpectedRevision)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return taskdomain.ErrRoadmapRevisionConflict
	}
	return nil
}
func (w *sqliteTaskDomainV2ProjectWriter) DeleteRoadmapNode(ctx context.Context, id string, rev int64) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	r, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_roadmap_nodes_v2 WHERE workspace_id=? AND id=? AND revision=?`, w.workspaceID, id, rev)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return taskdomain.ErrRoadmapRevisionConflict
	}
	return nil
}
