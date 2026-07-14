package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type calendarRepository struct {
	db sqliteRunner
}

func (r calendarRepository) ListProjectSources(ctx context.Context) (*model.CalendarProjectSourcesResponse, error) {
	workspaceID, userID, err := calendarScope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.name,
			p.type,
			CASE WHEN p.id = 'personal' THEN 1 ELSE COALESCE(cps.enabled, 0) END AS enabled,
			CASE WHEN p.id = 'personal' THEN 1 ELSE 0 END AS is_default,
			COALESCE(cps.color, '') AS color,
			COALESCE(cps.order_index, 0) AS order_index
		FROM task_projects p
		LEFT JOIN calendar_project_sources cps
			ON cps.workspace_id = p.workspace_id
			AND cps.project_id = p.id
			AND cps.user_id = ?
		WHERE p.workspace_id = ?
			AND (p.id = 'personal' OR p.type IN ('regular', 'learning'))
		ORDER BY
			CASE WHEN p.id = 'personal' THEN 0 WHEN COALESCE(cps.enabled, 0) = 1 THEN 1 ELSE 2 END,
			COALESCE(cps.order_index, 0),
			lower(p.name),
			p.id
	`, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	response := &model.CalendarProjectSourcesResponse{
		Sources:           []model.CalendarProjectSource{},
		AvailableProjects: []model.CalendarProjectSource{},
	}
	for rows.Next() {
		source, err := scanSQLiteCalendarProjectSource(rows)
		if err != nil {
			return nil, err
		}
		if source.Default || source.Enabled {
			response.Sources = append(response.Sources, source)
			continue
		}
		response.AvailableProjects = append(response.AvailableProjects, source)
	}
	return response, rows.Err()
}

func (r calendarRepository) SaveProjectSources(ctx context.Context, inputs []model.CalendarProjectSourceInput) (*model.CalendarProjectSourcesResponse, error) {
	workspaceID, userID, err := calendarScope(ctx)
	if err != nil {
		return nil, err
	}

	var response *model.CalendarProjectSourcesResponse
	if err := r.withTx(ctx, func(tx sqliteRunner) error {
		txRepo := calendarRepository{db: tx}
		for _, input := range inputs {
			projectID := strings.TrimSpace(input.ProjectID)
			if projectID == "" || projectID == "personal" {
				continue
			}
			enabled := 0
			if input.Enabled {
				enabled = 1
			}
			now := nowUnix()
			result, err := tx.ExecContext(ctx, `
				INSERT INTO calendar_project_sources (
					workspace_id, user_id, project_id, enabled, color, order_index, created_at, updated_at
				)
				SELECT ?, ?, p.id, ?, ?, ?, ?, ?
				FROM task_projects p
				WHERE p.workspace_id = ?
					AND p.id = ?
					AND p.type IN ('regular', 'learning')
				ON CONFLICT(workspace_id, user_id, project_id) DO UPDATE SET
					enabled = excluded.enabled,
					color = excluded.color,
					order_index = excluded.order_index,
					updated_at = excluded.updated_at
			`, workspaceID, userID, enabled, strings.TrimSpace(input.Color), input.OrderIndex, now, now, workspaceID, projectID)
			if err != nil {
				return fmt.Errorf("save calendar project source %s: %w", projectID, err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("save calendar project source %s: read affected rows: %w", projectID, err)
			}
			if affected == 0 {
				return fmt.Errorf("calendar project source %s is not a valid project in this workspace", projectID)
			}
		}
		var err error
		response, err = txRepo.ListProjectSources(ctx)
		return err
	}); err != nil {
		return nil, err
	}
	return response, nil
}

func (r calendarRepository) withTx(ctx context.Context, fn func(sqliteRunner) error) error {
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

func calendarScope(ctx context.Context) (string, string, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return "", "", err
	}
	userID, err := auth.UserIDFromContext(ctx)
	if err != nil {
		return "", "", err
	}
	return workspaceID, userID, nil
}

func scanSQLiteCalendarProjectSource(row interface {
	Scan(...interface{}) error
}) (model.CalendarProjectSource, error) {
	var source model.CalendarProjectSource
	var enabled int
	var isDefault int
	if err := row.Scan(
		&source.ProjectID,
		&source.Name,
		&source.Type,
		&enabled,
		&isDefault,
		&source.Color,
		&source.OrderIndex,
	); err != nil {
		return model.CalendarProjectSource{}, err
	}
	source.Enabled = enabled != 0
	source.Default = isDefault != 0
	return source, nil
}
