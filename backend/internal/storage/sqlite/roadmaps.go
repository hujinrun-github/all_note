package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type roadmapRepository struct {
	db sqliteRunner
}

func (r roadmapRepository) ReplaceLearningRoadmap(ctx context.Context, roadmap *model.LearningRoadmap) (*model.LearningRoadmap, error) {
	if roadmap == nil {
		return nil, fmt.Errorf("roadmap is nil")
	}
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	r.applyRoadmapDefaults(roadmap)
	if err := r.withTx(ctx, func(tx sqliteRunner) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM learning_roadmaps WHERE workspace_id = ? AND project_id = ?`, workspaceID, roadmap.ProjectID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO learning_roadmaps (id, project_id, workspace_id, title, goal, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, roadmap.ID, roadmap.ProjectID, workspaceID, roadmap.Title, roadmap.Goal, roadmap.Status, roadmap.CreatedAt, roadmap.UpdatedAt); err != nil {
			return err
		}
		for index := range roadmap.Nodes {
			node := &roadmap.Nodes[index]
			r.applyNodeDefaults(roadmap.ID, node, index)
			if err := insertSQLiteRoadmapNode(ctx, tx, node, nil); err != nil {
				return err
			}
		}
		for index := range roadmap.Nodes {
			node := &roadmap.Nodes[index]
			if node.ParentID == nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE roadmap_nodes SET parent_id = ?, updated_at = ? WHERE id = ? AND roadmap_id = ?
			`, *node.ParentID, node.UpdatedAt, node.ID, roadmap.ID); err != nil {
				return err
			}
		}
		for index := range roadmap.Edges {
			edge := &roadmap.Edges[index]
			r.applyEdgeDefaults(roadmap.ID, edge)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
				VALUES (?, ?, ?, ?, ?, ?)
			`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, edge.Style, edge.CreatedAt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return r.GetLearningRoadmap(ctx, roadmap.ProjectID)
}

func (r roadmapRepository) SaveFailedLearningRoadmap(ctx context.Context, projectID, title, goal string) (*model.LearningRoadmap, error) {
	now := nowUnix()
	if strings.TrimSpace(title) == "" {
		title = "学习路线生成失败"
	}
	roadmap := &model.LearningRoadmap{
		ID:        newID(),
		ProjectID: projectID,
		Title:     title,
		Goal:      goal,
		Status:    "failed",
		CreatedAt: now,
		UpdatedAt: now,
	}
	return r.ReplaceLearningRoadmap(ctx, roadmap)
}

func (r roadmapRepository) GetLearningRoadmap(ctx context.Context, projectID string) (*model.LearningRoadmap, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return r.loadLearningRoadmap(ctx, `WHERE workspace_id = ? AND project_id = ?`, workspaceID, projectID)
}

func (r roadmapRepository) GetLearningRoadmapByID(ctx context.Context, roadmapID string) (*model.LearningRoadmap, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return r.loadLearningRoadmap(ctx, `WHERE workspace_id = ? AND id = ?`, workspaceID, roadmapID)
}

func (r roadmapRepository) loadLearningRoadmap(ctx context.Context, where string, args ...interface{}) (*model.LearningRoadmap, error) {
	var roadmap model.LearningRoadmap
	err := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, project_id, title, goal, status, created_at, updated_at
		FROM learning_roadmaps %s
	`, where), args...).Scan(&roadmap.ID, &roadmap.ProjectID, &roadmap.Title, &roadmap.Goal, &roadmap.Status, &roadmap.CreatedAt, &roadmap.UpdatedAt)
	if err != nil {
		return nil, err
	}
	nodes, err := r.ListRoadmapNodes(ctx, roadmap.ID)
	if err != nil {
		return nil, err
	}
	edges, err := r.ListRoadmapEdges(ctx, roadmap.ID)
	if err != nil {
		return nil, err
	}
	roadmap.Nodes = nodes
	roadmap.Edges = edges
	return &roadmap, nil
}

func (r roadmapRepository) ListRoadmapNodes(ctx context.Context, roadmapID string) ([]model.RoadmapNode, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, article_search_queries, created_at, updated_at
		FROM roadmap_nodes
		WHERE roadmap_id = ?
		ORDER BY order_index ASC, created_at ASC
	`, roadmapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes, err := scanSQLiteRoadmapNodes(rows)
	if err != nil {
		return nil, err
	}
	for index := range nodes {
		resources, err := r.ListRoadmapResources(ctx, nodes[index].ID)
		if err != nil {
			return nil, err
		}
		nodes[index].Resources = resources
	}
	return nodes, nil
}

func (r roadmapRepository) ListRoadmapEdges(ctx context.Context, roadmapID string) ([]model.RoadmapEdge, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, roadmap_id, source_node_id, target_node_id, style, created_at
		FROM roadmap_edges
		WHERE roadmap_id = ?
		ORDER BY created_at ASC
	`, roadmapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := make([]model.RoadmapEdge, 0)
	for rows.Next() {
		var edge model.RoadmapEdge
		if err := rows.Scan(&edge.ID, &edge.RoadmapID, &edge.SourceNodeID, &edge.TargetNodeID, &edge.Style, &edge.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (r roadmapRepository) GetRoadmapNode(ctx context.Context, id string) (*model.RoadmapNode, error) {
	node, err := scanSQLiteRoadmapNode(r.db.QueryRowContext(ctx, `
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, article_search_queries, created_at, updated_at
		FROM roadmap_nodes WHERE id = ?
	`, id))
	if err != nil {
		return nil, err
	}
	resources, err := r.ListRoadmapResources(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	node.Resources = resources
	return node, nil
}

func (r roadmapRepository) CreateRoadmapNode(ctx context.Context, node *model.RoadmapNode, edge *model.RoadmapEdge) (*model.RoadmapNode, error) {
	if node == nil {
		return nil, fmt.Errorf("roadmap node is nil")
	}
	r.applyNodeDefaults(node.RoadmapID, node, 0)
	if err := r.withTx(ctx, func(tx sqliteRunner) error {
		if err := insertSQLiteRoadmapNode(ctx, tx, node, node.ParentID); err != nil {
			return err
		}
		if edge == nil {
			return nil
		}
		r.applyEdgeDefaults(node.RoadmapID, edge)
		if edge.TargetNodeID == "" {
			edge.TargetNodeID = node.ID
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, edge.Style, edge.CreatedAt)
		return err
	}); err != nil {
		return nil, err
	}
	return r.GetRoadmapNode(ctx, node.ID)
}

func (r roadmapRepository) UpdateRoadmapNode(ctx context.Context, id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	sets := []string{"updated_at = ?"}
	args := []interface{}{nowUnix()}
	if req.Title != nil {
		sets = append(sets, "title = ?")
		args = append(args, strings.TrimSpace(*req.Title))
	}
	if req.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*req.Description))
	}
	if req.PathType != nil {
		sets = append(sets, "path_type = ?")
		args = append(args, normalizePathType(*req.PathType))
	}
	if req.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, normalizeNodeStatus(*req.Status))
	}
	if req.Deliverable != nil {
		sets = append(sets, "deliverable = ?")
		args = append(args, strings.TrimSpace(*req.Deliverable))
	}
	if req.AcceptanceCriteria != nil {
		sets = append(sets, "acceptance_criteria = ?")
		args = append(args, strings.TrimSpace(*req.AcceptanceCriteria))
	}
	if req.X != nil {
		sets = append(sets, "x = ?")
		args = append(args, *req.X)
	}
	if req.Y != nil {
		sets = append(sets, "y = ?")
		args = append(args, *req.Y)
	}
	args = append(args, id)
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE roadmap_nodes SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}
	return r.GetRoadmapNode(ctx, id)
}

func (r roadmapRepository) DeleteRoadmapNode(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM roadmap_nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r roadmapRepository) UpdateRoadmapNodeStatus(ctx context.Context, id, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE roadmap_nodes SET status = ?, updated_at = ? WHERE id = ?
	`, normalizeNodeStatus(status), nowUnix(), id)
	return err
}

func (r roadmapRepository) UpdateRoadmapLayout(ctx context.Context, roadmapID string, nodes []model.RoadmapLayoutNode) error {
	return r.withTx(ctx, func(tx sqliteRunner) error {
		for _, node := range nodes {
			if _, err := tx.ExecContext(ctx, `
				UPDATE roadmap_nodes SET x = ?, y = ?, updated_at = ? WHERE id = ? AND roadmap_id = ?
			`, node.X, node.Y, nowUnix(), node.ID, roadmapID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r roadmapRepository) ListRoadmapResources(ctx context.Context, nodeID string) ([]model.RoadmapResource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, node_id, title, url, summary, source_type, added_by, created_at, updated_at
		FROM roadmap_resources
		WHERE node_id = ?
		ORDER BY created_at DESC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSQLiteRoadmapResources(rows)
}

func (r roadmapRepository) AddRoadmapResource(ctx context.Context, resource *model.RoadmapResource) error {
	if resource == nil {
		return fmt.Errorf("roadmap resource is nil")
	}
	resource.ID = newID()
	now := nowUnix()
	resource.CreatedAt = now
	resource.UpdatedAt = now
	if resource.SourceType == "" {
		resource.SourceType = "manual"
	}
	if resource.AddedBy == "" {
		resource.AddedBy = "user"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, resource.ID, resource.NodeID, resource.Title, resource.URL, resource.Summary, resource.SourceType, resource.AddedBy, now, now)
	return err
}

func (r roadmapRepository) DeleteRoadmapResource(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM roadmap_resources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r roadmapRepository) applyRoadmapDefaults(roadmap *model.LearningRoadmap) {
	if roadmap.ID == "" {
		roadmap.ID = newID()
	}
	now := nowUnix()
	if roadmap.CreatedAt == 0 {
		roadmap.CreatedAt = now
	}
	roadmap.UpdatedAt = now
	if roadmap.Status == "" {
		roadmap.Status = "ready"
	}
}

func (r roadmapRepository) applyNodeDefaults(roadmapID string, node *model.RoadmapNode, index int) {
	if node.ID == "" {
		node.ID = newID()
	}
	node.RoadmapID = roadmapID
	if node.Type == "" {
		node.Type = "task"
	}
	node.Type = normalizeRoadmapNodeType(node.Type)
	if node.PathType == "" {
		node.PathType = "required"
	}
	node.PathType = normalizePathType(node.PathType)
	if node.Status == "" {
		node.Status = "todo"
	}
	node.Status = normalizeNodeStatus(node.Status)
	if node.OrderIndex == 0 {
		node.OrderIndex = index
	}
	now := nowUnix()
	if node.CreatedAt == 0 {
		node.CreatedAt = now
	}
	node.UpdatedAt = now
}

func (r roadmapRepository) applyEdgeDefaults(roadmapID string, edge *model.RoadmapEdge) {
	if edge.ID == "" {
		edge.ID = newID()
	}
	edge.RoadmapID = roadmapID
	if edge.Style == "" {
		edge.Style = "solid"
	}
	edge.Style = normalizeEdgeStyle(edge.Style)
	if edge.CreatedAt == 0 {
		edge.CreatedAt = nowUnix()
	}
}

func (r roadmapRepository) withTx(ctx context.Context, fn func(sqliteRunner) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported sqlite runner %T", r.db)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func insertSQLiteRoadmapNode(ctx context.Context, db sqliteRunner, node *model.RoadmapNode, parentID *string) error {
	queries, err := roadmapQueriesToJSON(node.ArticleSearchQueries)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO roadmap_nodes (
			id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, article_search_queries, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.ID, node.RoadmapID, parentID, node.Type, strings.TrimSpace(node.Title), strings.TrimSpace(node.Description),
		node.PathType, node.Status, strings.TrimSpace(node.Deliverable), strings.TrimSpace(node.AcceptanceCriteria),
		node.X, node.Y, node.OrderIndex, queries, node.CreatedAt, node.UpdatedAt)
	return err
}

func scanSQLiteRoadmapNodes(rows *sql.Rows) ([]model.RoadmapNode, error) {
	nodes := make([]model.RoadmapNode, 0)
	for rows.Next() {
		node, err := scanSQLiteRoadmapNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}
	return nodes, rows.Err()
}

func scanSQLiteRoadmapNode(row sqliteRowScanner) (*model.RoadmapNode, error) {
	var node model.RoadmapNode
	var parentID sql.NullString
	var queries string
	if err := row.Scan(&node.ID, &node.RoadmapID, &parentID, &node.Type, &node.Title, &node.Description, &node.PathType, &node.Status,
		&node.Deliverable, &node.AcceptanceCriteria, &node.X, &node.Y, &node.OrderIndex, &queries, &node.CreatedAt, &node.UpdatedAt); err != nil {
		return nil, err
	}
	if parentID.Valid {
		node.ParentID = &parentID.String
	}
	node.ArticleSearchQueries = roadmapQueriesFromJSON(queries)
	return &node, nil
}

func scanSQLiteRoadmapResources(rows *sql.Rows) ([]model.RoadmapResource, error) {
	resources := make([]model.RoadmapResource, 0)
	for rows.Next() {
		var resource model.RoadmapResource
		if err := rows.Scan(&resource.ID, &resource.NodeID, &resource.Title, &resource.URL, &resource.Summary, &resource.SourceType, &resource.AddedBy, &resource.CreatedAt, &resource.UpdatedAt); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func roadmapQueriesToJSON(queries []string) (string, error) {
	if queries == nil {
		queries = []string{}
	}
	normalized := make([]string, 0, len(queries))
	for _, query := range queries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func roadmapQueriesFromJSON(raw string) []string {
	var queries []string
	if err := json.Unmarshal([]byte(raw), &queries); err != nil {
		return []string{}
	}
	if queries == nil {
		return []string{}
	}
	return queries
}

func normalizePathType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "recommended":
		return "recommended"
	case "optional":
		return "optional"
	case "alternative":
		return "alternative"
	default:
		return "required"
	}
}

func normalizeNodeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		return "active"
	case "done":
		return "done"
	case "skipped":
		return "skipped"
	default:
		return "todo"
	}
}

func normalizeRoadmapNodeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "phase":
		return "phase"
	case "module":
		return "module"
	case "choice":
		return "choice"
	default:
		return "task"
	}
}

func normalizeEdgeStyle(value string) string {
	if strings.ToLower(strings.TrimSpace(value)) == "dotted" {
		return "dotted"
	}
	return "solid"
}
