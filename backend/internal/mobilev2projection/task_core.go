package mobilev2projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

func projectTaskCore(
	ctx context.Context,
	runner Runner,
	dialect Dialect,
	projection Projection,
) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	appenders := []func(context.Context, Runner, Dialect, Projection, []json.RawMessage) ([]json.RawMessage, error){
		appendProjects,
		appendTasks,
		appendTaskSchedules,
		appendScheduleVersions,
		appendRoadmaps,
		appendRoadmapNodes,
		appendRoadmapEdges,
		appendRoadmapNodeProgress,
	}
	var err error
	for _, appendEntities := range appenders {
		result, err = appendEntities(ctx, runner, dialect, projection, result)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func appendProjects(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		id,name,description,kind,horizon,status,archived_from_status,system_role,target_at,
		revision,created_at,updated_at,archived_at
		FROM domain_projects_v2 WHERE workspace_id=? ORDER BY id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, name, description, kind, horizon, status string
			archivedFromStatus, systemRole               sql.NullString
			targetAt, createdAt, updatedAt, archivedAt   flexibleInstant
			projectRevision                              int64
		)
		if err := rows.Scan(
			&id, &name, &description, &kind, &horizon, &status, &archivedFromStatus, &systemRole, &targetAt,
			&projectRevision, &createdAt, &updatedAt, &archivedAt,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "project", EntityID: id, EntityRevision: strconv.FormatInt(projectRevision, 10),
			AggregateRevisions: aggregateRevisions{ProjectRevision: revision(projectRevision)},
			Payload: map[string]any{
				"name": name, "description": description, "kind": kind, "horizon": horizon, "status": status,
				"archived_from_status": optionalString(archivedFromStatus), "system_role": optionalString(systemRole),
				"target_at": instantString(targetAt), "created_at": requiredInstantString(createdAt),
				"updated_at": requiredInstantString(updatedAt), "archived_at": instantString(archivedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendTasks(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		t.id,t.project_id,t.roadmap_node_id,t.note_id,t.title,t.description,t.lifecycle_status,
		t.priority,t.sort_order,t.revision,t.created_at,t.updated_at,t.archived_at,
		p.revision,s.revision
		FROM domain_tasks_v2 t
		JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		LEFT JOIN domain_task_schedules_v2 s ON s.workspace_id=t.workspace_id AND s.task_id=t.id
		WHERE t.workspace_id=? ORDER BY t.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, projectID, title, description, lifecycleStatus string
			roadmapNodeID, noteID                              sql.NullString
			priority                                           int
			sortOrder                                          float64
			taskRevision, projectRevision                      int64
			scheduleRevision                                   sql.NullInt64
			createdAt, updatedAt, archivedAt                   flexibleInstant
		)
		if err := rows.Scan(
			&id, &projectID, &roadmapNodeID, &noteID, &title, &description, &lifecycleStatus,
			&priority, &sortOrder, &taskRevision, &createdAt, &updatedAt, &archivedAt,
			&projectRevision, &scheduleRevision,
		); err != nil {
			return nil, err
		}
		var scheduleRevisionValue *string
		if scheduleRevision.Valid {
			scheduleRevisionValue = revision(scheduleRevision.Int64)
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "task", EntityID: id, EntityRevision: strconv.FormatInt(taskRevision, 10),
			AggregateRevisions: aggregateRevisions{
				ProjectRevision: revision(projectRevision), TaskRevision: revision(taskRevision),
				ScheduleRevision: scheduleRevisionValue,
			},
			Payload: map[string]any{
				"project_id": projectID, "roadmap_node_id": optionalString(roadmapNodeID),
				"note_id": optionalString(noteID), "title": title, "description": description,
				"lifecycle_status": lifecycleStatus, "priority": priority, "sort_order": sortOrder,
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
				"archived_at": instantString(archivedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendTaskSchedules(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		s.task_id,s.revision,s.current_schedule_revision,s.generation_watermark,s.generation_status,
		s.generation_error,s.generation_retry_at,s.updated_at,t.revision,p.revision
		FROM domain_task_schedules_v2 s
		JOIN domain_tasks_v2 t ON t.workspace_id=s.workspace_id AND t.id=s.task_id
		JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		WHERE s.workspace_id=? ORDER BY s.task_id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			taskID, generationStatus                  string
			scheduleRevision, currentScheduleRevision int64
			taskRevision, projectRevision             int64
			generationWatermark, generationError      sql.NullString
			generationRetryAt, updatedAt              flexibleInstant
		)
		if err := rows.Scan(
			&taskID, &scheduleRevision, &currentScheduleRevision, &generationWatermark, &generationStatus,
			&generationError, &generationRetryAt, &updatedAt, &taskRevision, &projectRevision,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "task_schedule", EntityID: taskID, EntityRevision: strconv.FormatInt(scheduleRevision, 10),
			AggregateRevisions: aggregateRevisions{
				ProjectRevision: revision(projectRevision), TaskRevision: revision(taskRevision),
				ScheduleRevision: revision(scheduleRevision),
			},
			Payload: map[string]any{
				"task_id": taskID, "current_schedule_revision": strconv.FormatInt(currentScheduleRevision, 10),
				"generation_watermark": optionalString(generationWatermark), "generation_status": generationStatus,
				"generation_error": optionalString(generationError), "generation_retry_at": instantString(generationRetryAt),
				"updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendScheduleVersions(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		v.task_id,v.schedule_revision,v.effective_from,v.effective_to,v.recurrence_type,v.timing_type,
		v.timezone,v.starts_on,v.ends_on,v.recurrence_rule,v.local_start_time,v.duration_minutes,v.created_at,
		s.revision,t.revision,p.revision
		FROM domain_task_schedule_versions_v2 v
		JOIN domain_task_schedules_v2 s ON s.workspace_id=v.workspace_id AND s.task_id=v.task_id
		JOIN domain_tasks_v2 t ON t.workspace_id=v.workspace_id AND t.id=v.task_id
		JOIN domain_projects_v2 p ON p.workspace_id=t.workspace_id AND p.id=t.project_id
		WHERE v.workspace_id=? ORDER BY v.task_id,v.schedule_revision`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			taskID, recurrenceType, timingType, timeZone string
			scheduleVersion, scheduleRevision            int64
			taskRevision, projectRevision                int64
			effectiveFrom, effectiveTo                   sql.NullString
			startsOn, endsOn, localStartTime             sql.NullString
			ruleRaw                                      []byte
			durationMinutes                              sql.NullInt64
			createdAt                                    flexibleInstant
		)
		if err := rows.Scan(
			&taskID, &scheduleVersion, &effectiveFrom, &effectiveTo, &recurrenceType, &timingType,
			&timeZone, &startsOn, &endsOn, &ruleRaw, &localStartTime, &durationMinutes, &createdAt,
			&scheduleRevision, &taskRevision, &projectRevision,
		); err != nil {
			return nil, err
		}
		rule, err := decodeObject(ruleRaw)
		if err != nil {
			return nil, err
		}
		var duration any
		if durationMinutes.Valid {
			duration = durationMinutes.Int64
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType:     "schedule_version",
			EntityID:       taskID + ":" + strconv.FormatInt(scheduleVersion, 10),
			EntityRevision: strconv.FormatInt(scheduleVersion, 10),
			AggregateRevisions: aggregateRevisions{
				ProjectRevision: revision(projectRevision), TaskRevision: revision(taskRevision),
				ScheduleRevision: revision(scheduleRevision),
			},
			Payload: map[string]any{
				"task_id": taskID, "schedule_revision": strconv.FormatInt(scheduleVersion, 10),
				"effective_from": optionalString(effectiveFrom), "effective_to": optionalString(effectiveTo),
				"recurrence_type": recurrenceType, "timing_type": timingType, "timezone": timeZone,
				"starts_on": optionalString(startsOn), "ends_on": optionalString(endsOn), "rule": rule,
				"local_start_time": optionalString(localStartTime), "duration_minutes": duration,
				"created_at": requiredInstantString(createdAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendRoadmaps(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		r.id,r.project_id,r.status,r.title,r.description,r.revision,r.created_at,r.updated_at,p.revision
		FROM domain_learning_roadmaps_v2 r
		JOIN domain_projects_v2 p ON p.workspace_id=r.workspace_id AND p.id=r.project_id
		WHERE r.workspace_id=? ORDER BY r.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, projectID, status, title, description string
			roadmapRevision, projectRevision          int64
			createdAt, updatedAt                      flexibleInstant
		)
		if err := rows.Scan(&id, &projectID, &status, &title, &description, &roadmapRevision, &createdAt, &updatedAt, &projectRevision); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "learning_roadmap", EntityID: id, EntityRevision: strconv.FormatInt(roadmapRevision, 10),
			AggregateRevisions: aggregateRevisions{ProjectRevision: revision(projectRevision)},
			Payload: map[string]any{
				"project_id": projectID, "status": status, "title": title, "description": description,
				"created_at": requiredInstantString(createdAt), "updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendRoadmapNodes(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		n.id,n.project_id,n.roadmap_id,n.parent_id,n.title,n.description,n.node_type,n.status,
		n.position,n.legacy_metadata,n.revision,n.created_at,n.updated_at,p.revision
		FROM domain_roadmap_nodes_v2 n
		JOIN domain_projects_v2 p ON p.workspace_id=n.workspace_id AND p.id=n.project_id
		WHERE n.workspace_id=? ORDER BY n.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, projectID, roadmapID, title, description, nodeType, status string
			parentID                                                       sql.NullString
			position                                                       float64
			legacyRaw                                                      []byte
			nodeRevision, projectRevision                                  int64
			createdAt, updatedAt                                           flexibleInstant
		)
		if err := rows.Scan(
			&id, &projectID, &roadmapID, &parentID, &title, &description, &nodeType, &status,
			&position, &legacyRaw, &nodeRevision, &createdAt, &updatedAt, &projectRevision,
		); err != nil {
			return nil, err
		}
		legacy, err := decodeObject(legacyRaw)
		if err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "roadmap_node", EntityID: id, EntityRevision: strconv.FormatInt(nodeRevision, 10),
			AggregateRevisions: aggregateRevisions{ProjectRevision: revision(projectRevision)},
			Payload: map[string]any{
				"project_id": projectID, "roadmap_id": roadmapID, "parent_id": optionalString(parentID),
				"title": title, "description": description, "node_type": nodeType, "status": status,
				"position": position, "legacy_metadata": legacy, "created_at": requiredInstantString(createdAt),
				"updated_at": requiredInstantString(updatedAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendRoadmapEdges(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		e.id,e.project_id,e.roadmap_id,e.from_node_id,e.to_node_id,e.edge_type,e.revision,e.created_at,p.revision
		FROM domain_roadmap_edges_v2 e
		JOIN domain_projects_v2 p ON p.workspace_id=e.workspace_id AND p.id=e.project_id
		WHERE e.workspace_id=? ORDER BY e.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id, projectID, roadmapID, fromNodeID, toNodeID, edgeType string
			edgeRevision, projectRevision                            int64
			createdAt                                                flexibleInstant
		)
		if err := rows.Scan(
			&id, &projectID, &roadmapID, &fromNodeID, &toNodeID, &edgeType, &edgeRevision, &createdAt, &projectRevision,
		); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "roadmap_edge", EntityID: id, EntityRevision: strconv.FormatInt(edgeRevision, 10),
			AggregateRevisions: aggregateRevisions{ProjectRevision: revision(projectRevision)},
			Payload: map[string]any{
				"project_id": projectID, "roadmap_id": roadmapID, "from_node_id": fromNodeID,
				"to_node_id": toNodeID, "edge_type": edgeType, "created_at": requiredInstantString(createdAt),
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}

func appendRoadmapNodeProgress(ctx context.Context, runner Runner, dialect Dialect, projection Projection, result []json.RawMessage) ([]json.RawMessage, error) {
	rows, err := runner.QueryContext(ctx, bind(dialect, `SELECT
		n.id,n.revision,
		COUNT(o.id),
		COALESCE(SUM(CASE WHEN o.execution_status='open' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN o.execution_status='active' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN o.execution_status='blocked' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN o.execution_status='done' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN o.execution_status='skipped' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN o.execution_status='cancelled' THEN 1 ELSE 0 END),0)
		FROM domain_roadmap_nodes_v2 n
		LEFT JOIN domain_tasks_v2 t
			ON t.workspace_id=n.workspace_id AND t.roadmap_node_id=n.id
		LEFT JOIN domain_task_occurrences_v2 o
			ON o.workspace_id=t.workspace_id AND o.task_id=t.id
		WHERE n.workspace_id=?
		GROUP BY n.id,n.revision ORDER BY n.id`), projection.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var nodeRevision, total, open, active, blocked, done, skipped, cancelled int64
		if err := rows.Scan(&id, &nodeRevision, &total, &open, &active, &blocked, &done, &skipped, &cancelled); err != nil {
			return nil, err
		}
		result, err = appendEnvelope(result, entityEnvelope{
			EntityType: "roadmap_node_progress", EntityID: id, EntityRevision: strconv.FormatInt(nodeRevision, 10),
			AggregateRevisions: aggregateRevisions{},
			Payload: map[string]any{
				"roadmap_node_id": id, "as_of_sequence": strconv.FormatUint(projection.Sequence, 10),
				"total": total, "open": open, "active": active, "blocked": blocked,
				"done": done, "skipped": skipped, "cancelled": cancelled,
			},
		})
		if err != nil {
			return nil, err
		}
	}
	return result, rows.Err()
}
