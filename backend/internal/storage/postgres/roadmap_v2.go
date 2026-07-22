package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func (r *postgresTaskDomainV2ProjectReader) GetRoadmapByProject(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getPostgresRoadmap(ctx, r.queryer, r.workspaceID, "project_id", id)
}
func (r *postgresTaskDomainV2ProjectReader) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getPostgresRoadmap(ctx, r.queryer, r.workspaceID, "id", id)
}
func getPostgresRoadmap(ctx context.Context, q postgresTaskDomainV2Queryer, w, column, value string) (taskdomain.RoadmapSnapshot, error) {
	var out taskdomain.RoadmapSnapshot
	var status string
	err := q.QueryRowContext(ctx, `SELECT workspace_id,id,project_id,status,title,description,revision FROM domain_learning_roadmaps_v2 WHERE workspace_id=$1 AND `+column+`=$2`, w, value).Scan(&out.Roadmap.WorkspaceID, &out.Roadmap.ID, &out.Roadmap.ProjectID, &status, &out.Roadmap.Title, &out.Roadmap.Description, &out.Roadmap.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, taskdomain.ErrRoadmapNotFound
	}
	if err != nil {
		return out, err
	}
	out.Roadmap.Status = taskdomain.RoadmapStatus(status)
	rows, err := q.QueryContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,position,revision FROM domain_roadmap_nodes_v2 WHERE workspace_id=$1 AND roadmap_id=$2 ORDER BY position,id`, w, out.Roadmap.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		n, err := scanPostgresRoadmapV2Node(rows)
		if err != nil {
			return out, err
		}
		n.Progress, err = postgresRoadmapProgress(ctx, q, w, n.Node.ID)
		if err != nil {
			return out, err
		}
		out.Nodes = append(out.Nodes, n)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	edges, err := q.QueryContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,from_node_id,to_node_id,edge_type,revision FROM domain_roadmap_edges_v2 WHERE workspace_id=$1 AND roadmap_id=$2 ORDER BY id`, w, out.Roadmap.ID)
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
func (r *postgresTaskDomainV2ProjectReader) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return getPostgresRoadmapNode(ctx, r.queryer, r.workspaceID, id)
}
func (w *postgresTaskDomainV2ProjectWriter) GetRoadmapByProject(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getPostgresRoadmap(ctx, w.queryer, w.workspaceID, "project_id", id)
}
func (w *postgresTaskDomainV2ProjectWriter) GetRoadmapByID(ctx context.Context, id string) (taskdomain.RoadmapSnapshot, error) {
	return getPostgresRoadmap(ctx, w.queryer, w.workspaceID, "id", id)
}
func (w *postgresTaskDomainV2ProjectWriter) GetRoadmapNode(ctx context.Context, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	return getPostgresRoadmapNode(ctx, w.queryer, w.workspaceID, id)
}

type postgresRoadmapScanner interface{ Scan(...any) error }

func scanPostgresRoadmapV2Node(s postgresRoadmapScanner) (taskdomain.RoadmapNodeSnapshot, error) {
	var n taskdomain.RoadmapNodeSnapshot
	var parent sql.NullString
	var typ string
	err := s.Scan(&n.Node.WorkspaceID, &n.Node.ID, &n.Node.ProjectID, &n.Node.RoadmapID, &parent, &n.Node.Title, &n.Node.Description, &typ, &n.Node.Position, &n.Node.Revision)
	n.Node.ParentID = parent.String
	n.Node.Type = taskdomain.RoadmapNodeType(typ)
	return n, err
}
func getPostgresRoadmapNode(ctx context.Context, q postgresTaskDomainV2Queryer, w, id string) (taskdomain.RoadmapNodeSnapshot, error) {
	n, err := scanPostgresRoadmapV2Node(q.QueryRowContext(ctx, `SELECT workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,position,revision FROM domain_roadmap_nodes_v2 WHERE workspace_id=$1 AND id=$2`, w, id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, taskdomain.ErrRoadmapNodeNotFound
	}
	if err != nil {
		return n, err
	}
	n.Progress, err = postgresRoadmapProgress(ctx, q, w, id)
	return n, err
}
func postgresRoadmapProgress(ctx context.Context, q postgresTaskDomainV2Queryer, w, node string) (taskdomain.RoadmapNodeProgress, error) {
	var p taskdomain.RoadmapNodeProgress
	err := q.QueryRowContext(ctx, `SELECT COUNT(DISTINCT t.id),COUNT(o.id),COALESCE(SUM(CASE WHEN o.execution_status='open' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='active' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='blocked' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='done' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='skipped' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN o.execution_status='cancelled' THEN 1 ELSE 0 END),0) FROM domain_tasks_v2 t LEFT JOIN domain_task_occurrences_v2 o ON o.workspace_id=t.workspace_id AND o.task_id=t.id WHERE t.workspace_id=$1 AND t.roadmap_node_id=$2`, w, node).Scan(&p.Tasks, &p.Total, &p.Open, &p.Active, &p.Blocked, &p.Done, &p.Skipped, &p.Cancelled)
	return p, err
}
func (w *postgresTaskDomainV2ProjectWriter) CountRoadmapNodeTasks(ctx context.Context, id string) (int, error) {
	if w.isClosed != nil && w.isClosed() {
		return 0, storage.ErrTenantWriteTxClosed
	}
	var n int
	err := w.queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_tasks_v2 WHERE workspace_id=$1 AND roadmap_node_id=$2`, w.workspaceID, id).Scan(&n)
	return n, err
}
func (w *postgresTaskDomainV2ProjectWriter) CreateRoadmap(ctx context.Context, r taskdomain.LearningRoadmap) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	_, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_learning_roadmaps_v2(workspace_id,id,project_id,status,title,description,revision,created_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,1,now(),now())`, w.workspaceID, r.ID, r.ProjectID, r.Status, r.Title, r.Description)
	return err
}
func (w *postgresTaskDomainV2ProjectWriter) CreateRoadmapNode(ctx context.Context, n taskdomain.RoadmapNode) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	_, err := w.queryer.ExecContext(ctx, `INSERT INTO domain_roadmap_nodes_v2(workspace_id,id,project_id,roadmap_id,parent_id,title,description,node_type,status,position,revision,created_at,updated_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,'available',$9,1,now(),now())`, w.workspaceID, n.ID, n.ProjectID, n.RoadmapID, nullablePostgresTaskDomainV2String(n.ParentID), n.Title, n.Description, n.Type, n.Position)
	return err
}
func (w *postgresTaskDomainV2ProjectWriter) SaveRoadmapNode(ctx context.Context, x taskdomain.RoadmapNodeWrite) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	r, err := w.queryer.ExecContext(ctx, `UPDATE domain_roadmap_nodes_v2 SET parent_id=$1,title=$2,description=$3,node_type=$4,position=$5,revision=revision+1,updated_at=now() WHERE workspace_id=$6 AND id=$7 AND revision=$8`, nullablePostgresTaskDomainV2String(x.Node.ParentID), x.Node.Title, x.Node.Description, x.Node.Type, x.Node.Position, w.workspaceID, x.Node.ID, x.ExpectedRevision)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return taskdomain.ErrRoadmapRevisionConflict
	}
	return nil
}
func (w *postgresTaskDomainV2ProjectWriter) DeleteRoadmapNode(ctx context.Context, id string, rev int64) error {
	if w.isClosed != nil && w.isClosed() {
		return storage.ErrTenantWriteTxClosed
	}
	r, err := w.queryer.ExecContext(ctx, `DELETE FROM domain_roadmap_nodes_v2 WHERE workspace_id=$1 AND id=$2 AND revision=$3`, w.workspaceID, id, rev)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return taskdomain.ErrRoadmapRevisionConflict
	}
	return nil
}
