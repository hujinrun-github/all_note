package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

func ReplaceLearningRoadmap(roadmap *model.LearningRoadmap) (*model.LearningRoadmap, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().ReplaceLearningRoadmap(context.Background(), roadmap)
	}

	if roadmap.ID == "" {
		roadmap.ID = newUUID()
	}
	now := nowUnix()
	roadmap.CreatedAt = now
	roadmap.UpdatedAt = now
	if roadmap.Status == "" {
		roadmap.Status = "ready"
	}

	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM learning_roadmaps WHERE project_id = ?`, roadmap.ProjectID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, roadmap.ID, roadmap.ProjectID, roadmap.Title, roadmap.Goal, roadmap.Status, now, now); err != nil {
		return nil, err
	}

	for index := range roadmap.Nodes {
		node := &roadmap.Nodes[index]
		if node.ID == "" {
			node.ID = newUUID()
		}
		node.RoadmapID = roadmap.ID
		node.CreatedAt = now
		node.UpdatedAt = now
		if node.Status == "" {
			node.Status = "todo"
		}
		if node.PathType == "" {
			node.PathType = "required"
		}
		if node.Type == "" {
			node.Type = "task"
		}
		if _, err := tx.Exec(`
			INSERT INTO roadmap_nodes (
				id, roadmap_id, parent_id, type, title, description, path_type, status,
				deliverable, acceptance_criteria, x, y, order_index, created_at, updated_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, node.ID, node.RoadmapID, node.ParentID, node.Type, node.Title, node.Description, node.PathType, node.Status,
			node.Deliverable, node.AcceptanceCriteria, node.X, node.Y, node.OrderIndex, now, now); err != nil {
			return nil, err
		}
	}

	for index := range roadmap.Edges {
		edge := &roadmap.Edges[index]
		if edge.ID == "" {
			edge.ID = newUUID()
		}
		edge.RoadmapID = roadmap.ID
		edge.CreatedAt = now
		if edge.Style == "" {
			edge.Style = "solid"
		}
		if _, err := tx.Exec(`
			INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, edge.Style, now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetLearningRoadmap(roadmap.ProjectID)
}

func SaveFailedLearningRoadmap(projectID, title, goal string) (*model.LearningRoadmap, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().SaveFailedLearningRoadmap(context.Background(), projectID, title, goal)
	}

	now := nowUnix()
	id := newUUID()
	if strings.TrimSpace(title) == "" {
		title = "学习路线生成失败"
	}
	if _, err := DB.Exec(`DELETE FROM learning_roadmaps WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	if _, err := DB.Exec(`
		INSERT INTO learning_roadmaps (id, project_id, title, goal, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'failed', ?, ?)
	`, id, projectID, title, goal, now, now); err != nil {
		return nil, err
	}
	return GetLearningRoadmap(projectID)
}

func GetLearningRoadmap(projectID string) (*model.LearningRoadmap, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().GetLearningRoadmap(context.Background(), projectID)
	}

	var roadmap model.LearningRoadmap
	err := DB.QueryRow(`
		SELECT id, project_id, title, goal, status, created_at, updated_at
		FROM learning_roadmaps
		WHERE project_id = ?
	`, projectID).Scan(&roadmap.ID, &roadmap.ProjectID, &roadmap.Title, &roadmap.Goal, &roadmap.Status, &roadmap.CreatedAt, &roadmap.UpdatedAt)
	if err != nil {
		return nil, err
	}

	nodes, err := ListRoadmapNodes(roadmap.ID)
	if err != nil {
		return nil, err
	}
	edges, err := ListRoadmapEdges(roadmap.ID)
	if err != nil {
		return nil, err
	}
	roadmap.Nodes = nodes
	roadmap.Edges = edges
	return &roadmap, nil
}

func GetLearningRoadmapByID(roadmapID string) (*model.LearningRoadmap, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().GetLearningRoadmapByID(context.Background(), roadmapID)
	}

	var roadmap model.LearningRoadmap
	err := DB.QueryRow(`
		SELECT id, project_id, title, goal, status, created_at, updated_at
		FROM learning_roadmaps
		WHERE id = ?
	`, roadmapID).Scan(&roadmap.ID, &roadmap.ProjectID, &roadmap.Title, &roadmap.Goal, &roadmap.Status, &roadmap.CreatedAt, &roadmap.UpdatedAt)
	if err != nil {
		return nil, err
	}

	nodes, err := ListRoadmapNodes(roadmap.ID)
	if err != nil {
		return nil, err
	}
	edges, err := ListRoadmapEdges(roadmap.ID)
	if err != nil {
		return nil, err
	}
	roadmap.Nodes = nodes
	roadmap.Edges = edges
	return &roadmap, nil
}

func ListRoadmapNodes(roadmapID string) ([]model.RoadmapNode, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().ListRoadmapNodes(context.Background(), roadmapID)
	}

	rows, err := DB.Query(`
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, created_at, updated_at
		FROM roadmap_nodes
		WHERE roadmap_id = ?
		ORDER BY order_index ASC, created_at ASC
	`, roadmapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := make([]model.RoadmapNode, 0)
	for rows.Next() {
		var node model.RoadmapNode
		if err := rows.Scan(&node.ID, &node.RoadmapID, &node.ParentID, &node.Type, &node.Title, &node.Description, &node.PathType, &node.Status,
			&node.Deliverable, &node.AcceptanceCriteria, &node.X, &node.Y, &node.OrderIndex, &node.CreatedAt, &node.UpdatedAt); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	for index := range nodes {
		resources, err := ListRoadmapResources(nodes[index].ID)
		if err != nil {
			return nil, err
		}
		nodes[index].Resources = resources
	}
	return nodes, nil
}

func ListRoadmapEdges(roadmapID string) ([]model.RoadmapEdge, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().ListRoadmapEdges(context.Background(), roadmapID)
	}

	rows, err := DB.Query(`
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

func GetRoadmapNode(nodeID string) (*model.RoadmapNode, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().GetRoadmapNode(context.Background(), nodeID)
	}

	var node model.RoadmapNode
	err := DB.QueryRow(`
		SELECT id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, created_at, updated_at
		FROM roadmap_nodes
		WHERE id = ?
	`, nodeID).Scan(&node.ID, &node.RoadmapID, &node.ParentID, &node.Type, &node.Title, &node.Description, &node.PathType, &node.Status,
		&node.Deliverable, &node.AcceptanceCriteria, &node.X, &node.Y, &node.OrderIndex, &node.CreatedAt, &node.UpdatedAt)
	if err != nil {
		return nil, err
	}
	resources, err := ListRoadmapResources(node.ID)
	if err != nil {
		return nil, err
	}
	node.Resources = resources
	return &node, nil
}

func CreateRoadmapNode(node *model.RoadmapNode, edge *model.RoadmapEdge) (*model.RoadmapNode, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().CreateRoadmapNode(context.Background(), node, edge)
	}

	if node.ID == "" {
		node.ID = newUUID()
	}
	now := nowUnix()
	node.CreatedAt = now
	node.UpdatedAt = now
	if node.Status == "" {
		node.Status = "todo"
	}
	if node.PathType == "" {
		node.PathType = "required"
	}
	if node.Type == "" {
		node.Type = "task"
	}

	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO roadmap_nodes (
			id, roadmap_id, parent_id, type, title, description, path_type, status,
			deliverable, acceptance_criteria, x, y, order_index, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, node.ID, node.RoadmapID, node.ParentID, normalizeRoadmapNodeType(node.Type), strings.TrimSpace(node.Title),
		strings.TrimSpace(node.Description), normalizePathType(node.PathType), normalizeNodeStatus(node.Status),
		strings.TrimSpace(node.Deliverable), strings.TrimSpace(node.AcceptanceCriteria), node.X, node.Y, node.OrderIndex, now, now); err != nil {
		return nil, err
	}

	if edge != nil {
		if edge.ID == "" {
			edge.ID = newUUID()
		}
		if edge.RoadmapID == "" {
			edge.RoadmapID = node.RoadmapID
		}
		if edge.TargetNodeID == "" {
			edge.TargetNodeID = node.ID
		}
		if edge.Style == "" {
			edge.Style = "solid"
		}
		if _, err := tx.Exec(`
			INSERT INTO roadmap_edges (id, roadmap_id, source_node_id, target_node_id, style, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, edge.ID, edge.RoadmapID, edge.SourceNodeID, edge.TargetNodeID, normalizeEdgeStyle(edge.Style), now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetRoadmapNode(node.ID)
}

func UpdateRoadmapNode(id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().UpdateRoadmapNode(context.Background(), id, req)
	}

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
	result, err := DB.Exec(fmt.Sprintf("UPDATE roadmap_nodes SET %s WHERE id = ?", strings.Join(sets, ", ")), args...)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}
	return GetRoadmapNode(id)
}

func DeleteRoadmapNode(id string) error {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().DeleteRoadmapNode(context.Background(), id)
	}

	result, err := DB.Exec(`DELETE FROM roadmap_nodes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func UpdateRoadmapNodeStatus(id, status string) error {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().UpdateRoadmapNodeStatus(context.Background(), id, status)
	}

	_, err := DB.Exec(`
		UPDATE roadmap_nodes
		SET status = ?, updated_at = ?
		WHERE id = ?
	`, normalizeNodeStatus(status), nowUnix(), id)
	return err
}

func UpdateRoadmapLayout(roadmapID string, nodes []model.RoadmapLayoutNode) error {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().UpdateRoadmapLayout(context.Background(), roadmapID, nodes)
	}

	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, node := range nodes {
		if _, err := tx.Exec(`
			UPDATE roadmap_nodes
			SET x = ?, y = ?, updated_at = ?
			WHERE id = ? AND roadmap_id = ?
		`, node.X, node.Y, nowUnix(), node.ID, roadmapID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func ListRoadmapResources(nodeID string) ([]model.RoadmapResource, error) {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().ListRoadmapResources(context.Background(), nodeID)
	}

	rows, err := DB.Query(`
		SELECT id, node_id, title, url, summary, source_type, added_by, created_at, updated_at
		FROM roadmap_resources
		WHERE node_id = ?
		ORDER BY created_at DESC
	`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

func AddRoadmapResource(resource *model.RoadmapResource) error {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().AddRoadmapResource(context.Background(), resource)
	}

	resource.ID = newUUID()
	now := nowUnix()
	resource.CreatedAt = now
	resource.UpdatedAt = now
	if resource.SourceType == "" {
		resource.SourceType = "manual"
	}
	if resource.AddedBy == "" {
		resource.AddedBy = "user"
	}
	_, err := DB.Exec(`
		INSERT INTO roadmap_resources (id, node_id, title, url, summary, source_type, added_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, resource.ID, resource.NodeID, resource.Title, resource.URL, resource.Summary, resource.SourceType, resource.AddedBy, now, now)
	return err
}

func DeleteRoadmapResource(id string) error {
	if store := CurrentStore(); store != nil {
		return store.Roadmaps().DeleteRoadmapResource(context.Background(), id)
	}

	result, err := DB.Exec(`DELETE FROM roadmap_resources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
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
