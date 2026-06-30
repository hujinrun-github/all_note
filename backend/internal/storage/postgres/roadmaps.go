package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/lib/pq"
)

type roadmapRepository struct {
	db postgresRunner
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
	if err := r.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM learning_roadmaps WHERE workspace_id = $1 AND project_id = $2`, workspaceID, roadmap.ProjectID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at, workspace_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, roadmap.ID, roadmap.ProjectID, roadmap.Title, roadmap.Goal, roadmap.Status, unixToTime(roadmap.CreatedAt), unixToTime(roadmap.UpdatedAt), workspaceID); err != nil {
			return err
		}
		for index := range roadmap.Nodes {
			node := &roadmap.Nodes[index]
			r.applyNodeDefaults(roadmap.ID, node, index)
			if err := insertPostgresRoadmapNode(ctx, tx, workspaceID, node, nil); err != nil {
				return err
			}
		}
		for index := range roadmap.Nodes {
			node := &roadmap.Nodes[index]
			if node.ParentID == nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE roadmap_nodes SET parent_id = $1, updated_at = $2 WHERE workspace_id = $3 AND id = $4 AND roadmap_id = $5
			`, *node.ParentID, unixToTime(node.UpdatedAt), workspaceID, node.ID, roadmap.ID); err != nil {
				return err
			}
		}
		for index := range roadmap.Edges {
			edge := &roadmap.Edges[index]
			r.applyEdgeDefaults(roadmap.ID, edge)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at, workspace_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, edge.Style, unixToTime(edge.CreatedAt), workspaceID); err != nil {
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
	return r.ReplaceLearningRoadmap(ctx, &model.LearningRoadmap{
		ID:        newID(),
		ProjectID: projectID,
		Title:     title,
		Goal:      goal,
		Status:    "failed",
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (r roadmapRepository) GetLearningRoadmap(ctx context.Context, projectID string) (*model.LearningRoadmap, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return r.loadLearningRoadmap(ctx, `WHERE workspace_id = $1 AND project_id = $2`, workspaceID, projectID)
}

func (r roadmapRepository) GetLearningRoadmapByID(ctx context.Context, roadmapID string) (*model.LearningRoadmap, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return r.loadLearningRoadmap(ctx, `WHERE workspace_id = $1 AND id = $2`, workspaceID, roadmapID)
}

func (r roadmapRepository) loadLearningRoadmap(ctx context.Context, where string, args ...interface{}) (*model.LearningRoadmap, error) {
	var roadmap model.LearningRoadmap
	var createdAt time.Time
	var updatedAt time.Time
	if err := r.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, project_id, title, goal, status, created_at, updated_at
		FROM learning_roadmaps %s
	`, where), args...).Scan(&roadmap.ID, &roadmap.ProjectID, &roadmap.Title, &roadmap.Goal, &roadmap.Status, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	roadmap.CreatedAt = timeToUnix(createdAt)
	roadmap.UpdatedAt = timeToUnix(updatedAt)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, postgresRoadmapNodeSelectSQL()+`
		WHERE workspace_id = $1 AND roadmap_id = $2
		ORDER BY order_index ASC, created_at ASC
	`, workspaceID, roadmapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes, err := scanPostgresRoadmapNodes(rows)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, roadmap_id, source_node_id, target_node_id, style, created_at
		FROM roadmap_edges
		WHERE workspace_id = $1 AND roadmap_id = $2
		ORDER BY created_at ASC
	`, workspaceID, roadmapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	edges := make([]model.RoadmapEdge, 0)
	for rows.Next() {
		var edge model.RoadmapEdge
		var createdAt time.Time
		if err := rows.Scan(&edge.ID, &edge.RoadmapID, &edge.SourceNodeID, &edge.TargetNodeID, &edge.Style, &createdAt); err != nil {
			return nil, err
		}
		edge.CreatedAt = timeToUnix(createdAt)
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (r roadmapRepository) GetRoadmapNode(ctx context.Context, id string) (*model.RoadmapNode, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	node, err := scanPostgresRoadmapNode(r.db.QueryRowContext(ctx, postgresRoadmapNodeSelectSQL()+` WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	r.applyNodeDefaults(node.RoadmapID, node, 0)
	if err := r.withTx(ctx, func(tx *sql.Tx) error {
		if err := insertPostgresRoadmapNode(ctx, tx, workspaceID, node, node.ParentID); err != nil {
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
			INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at, workspace_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, edge.Style, unixToTime(edge.CreatedAt), workspaceID)
		return err
	}); err != nil {
		return nil, err
	}
	return r.GetRoadmapNode(ctx, node.ID)
}

func (r roadmapRepository) UpdateRoadmapNode(ctx context.Context, id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	builder := newPgSetBuilder(1)
	builder.Add("updated_at", time.Now().UTC())
	if req.Title != nil {
		builder.Add("title", strings.TrimSpace(*req.Title))
	}
	if req.Description != nil {
		builder.Add("description", strings.TrimSpace(*req.Description))
	}
	if req.PathType != nil {
		builder.Add("path_type", normalizePathType(*req.PathType))
	}
	if req.Status != nil {
		builder.Add("status", normalizeNodeStatus(*req.Status))
	}
	if req.Deliverable != nil {
		builder.Add("deliverable", strings.TrimSpace(*req.Deliverable))
	}
	if req.AcceptanceCriteria != nil {
		builder.Add("acceptance_criteria", strings.TrimSpace(*req.AcceptanceCriteria))
	}
	if req.X != nil || req.Y != nil {
		current, err := r.GetRoadmapNode(ctx, id)
		if err != nil {
			return nil, err
		}
		x, y := current.X, current.Y
		if req.X != nil {
			x = *req.X
		}
		if req.Y != nil {
			y = *req.Y
		}
		position, err := roadmapPositionJSON(x, y)
		if err != nil {
			return nil, err
		}
		builder.Add("position", position)
	}
	clause, args := builder.ClauseAndArgs()
	args = append(args, workspaceID, id)
	result, err := r.db.ExecContext(ctx, fmt.Sprintf("UPDATE roadmap_nodes SET %s WHERE workspace_id = %s AND id = %s", clause, pgPlaceholder(len(args)-1), pgPlaceholder(len(args))), args...)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}
	return r.GetRoadmapNode(ctx, id)
}

func (r roadmapRepository) DeleteRoadmapNode(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM roadmap_nodes WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r roadmapRepository) UpdateRoadmapNodeStatus(ctx context.Context, id, status string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE roadmap_nodes SET status = $1, updated_at = $2 WHERE workspace_id = $3 AND id = $4
	`, normalizeNodeStatus(status), time.Now().UTC(), workspaceID, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r roadmapRepository) UpdateRoadmapLayout(ctx context.Context, roadmapID string, nodes []model.RoadmapLayoutNode) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	return r.withTx(ctx, func(tx *sql.Tx) error {
		for _, node := range nodes {
			position, err := roadmapPositionJSON(node.X, node.Y)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE roadmap_nodes SET position = $1, updated_at = $2 WHERE workspace_id = $3 AND id = $4 AND roadmap_id = $5
			`, position, time.Now().UTC(), workspaceID, node.ID, roadmapID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r roadmapRepository) ListRoadmapResources(ctx context.Context, nodeID string) ([]model.RoadmapResource, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, node_id, title, url, summary, source_type, added_by, created_at, updated_at
		FROM roadmap_resources
		WHERE workspace_id = $1 AND node_id = $2
		ORDER BY created_at DESC
	`, workspaceID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostgresRoadmapResources(rows)
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
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, resource.ID, resource.NodeID, resource.Title, resource.URL, resource.Summary, resource.SourceType, resource.AddedBy, unixToTime(now), unixToTime(now), workspaceID)
	return err
}

func (r roadmapRepository) DeleteRoadmapResource(ctx context.Context, id string) error {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM roadmap_resources WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
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

func (r roadmapRepository) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if tx, ok := r.db.(*sql.Tx); ok {
		return fn(tx)
	}
	db, ok := r.db.(*sql.DB)
	if !ok {
		return fmt.Errorf("unsupported postgres runner %T", r.db)
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

func insertPostgresRoadmapNode(ctx context.Context, tx *sql.Tx, workspaceID string, node *model.RoadmapNode, parentID *string) error {
	position, err := roadmapPositionJSON(node.X, node.Y)
	if err != nil {
		return err
	}
	queries := normalizeRoadmapQueries(node.ArticleSearchQueries)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO roadmap_nodes (
			id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, position, order_index, article_search_queries, created_at, updated_at, workspace_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13::text[], $14, $15, $16)
	`, node.ID, node.RoadmapID, parentID, node.Type, strings.TrimSpace(node.Title), strings.TrimSpace(node.Description),
		node.PathType, node.Status, strings.TrimSpace(node.Deliverable), strings.TrimSpace(node.AcceptanceCriteria),
		position, node.OrderIndex, pq.Array(queries), unixToTime(node.CreatedAt), unixToTime(node.UpdatedAt), workspaceID)
	return err
}

func postgresRoadmapNodeSelectSQL() string {
	return `
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, position, order_index, article_search_queries, created_at, updated_at
		FROM roadmap_nodes
	`
}

func scanPostgresRoadmapNodes(rows *sql.Rows) ([]model.RoadmapNode, error) {
	nodes := make([]model.RoadmapNode, 0)
	for rows.Next() {
		node, err := scanPostgresRoadmapNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}
	return nodes, rows.Err()
}

func scanPostgresRoadmapNode(row rowScanner) (*model.RoadmapNode, error) {
	var node model.RoadmapNode
	var parentID sql.NullString
	var position []byte
	var queries []string
	var createdAt time.Time
	var updatedAt time.Time
	if err := row.Scan(&node.ID, &node.RoadmapID, &parentID, &node.Type, &node.Title, &node.Description, &node.PathType, &node.Status,
		&node.Deliverable, &node.AcceptanceCriteria, &position, &node.OrderIndex, pq.Array(&queries), &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if parentID.Valid {
		node.ParentID = &parentID.String
	}
	x, y := roadmapPositionFromJSON(position)
	node.X = x
	node.Y = y
	node.ArticleSearchQueries = queries
	if node.ArticleSearchQueries == nil {
		node.ArticleSearchQueries = []string{}
	}
	node.CreatedAt = timeToUnix(createdAt)
	node.UpdatedAt = timeToUnix(updatedAt)
	return &node, nil
}

func scanPostgresRoadmapResources(rows *sql.Rows) ([]model.RoadmapResource, error) {
	resources := make([]model.RoadmapResource, 0)
	for rows.Next() {
		var resource model.RoadmapResource
		var createdAt time.Time
		var updatedAt time.Time
		if err := rows.Scan(&resource.ID, &resource.NodeID, &resource.Title, &resource.URL, &resource.Summary, &resource.SourceType, &resource.AddedBy, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		resource.CreatedAt = timeToUnix(createdAt)
		resource.UpdatedAt = timeToUnix(updatedAt)
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func roadmapPositionJSON(x, y float64) (string, error) {
	data, err := json.Marshal(map[string]float64{"x": x, "y": y})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func roadmapPositionFromJSON(raw []byte) (float64, float64) {
	var position struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := json.Unmarshal(raw, &position); err != nil {
		return 0, 0
	}
	return position.X, position.Y
}

func normalizeRoadmapQueries(queries []string) []string {
	normalized := make([]string, 0, len(queries))
	for _, query := range queries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
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
