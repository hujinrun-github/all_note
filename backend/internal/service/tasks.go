package service

import (
	"context"
	"fmt"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

// RecurringTaskError is a typed error for recurring task validation failures.
type RecurringTaskError struct {
	Code    string
	Message string
}

func (e *RecurringTaskError) Error() string {
	return e.Message
}

func GetTasks(ctx context.Context, store storage.Store, project, status, scope, horizon, projectID, plannedDate, plannedFrom, plannedTo, executionType string, page, pageSize int) ([]model.Task, int, error) {
	return store.Tasks().List(ctx, storage.TaskFilter{
		Project:       project,
		Status:        status,
		Scope:         scope,
		Horizon:       horizon,
		ProjectID:     projectID,
		PlannedDate:   plannedDate,
		PlannedFrom:   plannedFrom,
		PlannedTo:     plannedTo,
		ExecutionType: executionType,
		Page:          page,
		PageSize:      pageSize,
	})
}

func GetTaskProjects(ctx context.Context, store storage.Store) ([]string, error) {
	projects, err := store.Tasks().ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return names, nil
}

func ListTaskProjects(ctx context.Context, store storage.Store) ([]model.TaskProject, error) {
	return store.Tasks().ListProjects(ctx)
}

func CreateTaskProject(ctx context.Context, store storage.Store, req *model.CreateTaskProjectRequest) (*model.TaskProject, error) {
	return store.Tasks().CreateProject(ctx, req)
}

func UpdateTaskProject(ctx context.Context, store storage.Store, id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
	return store.Tasks().UpdateProject(ctx, id, req)
}

func DeleteTaskProject(ctx context.Context, store storage.Store, id string) error {
	return store.Tasks().DeleteProject(ctx, id)
}

func CreateTask(ctx context.Context, store storage.Store, req *model.CreateTaskRequest) (*model.Task, error) {
	if req.ExecutionType == "recurring" {
		if req.Recurrence == nil {
			return nil, fmt.Errorf("recurrence config is required for recurring task")
		}
		if err := ValidateRecurrenceConfig(req.Recurrence); err != nil {
			return nil, err
		}
		var task *model.Task
		err := store.Transact(ctx, func(txStore storage.Store) error {
			t := &model.Task{
				Title:         req.Title,
				Content:       req.Content,
				Project:       req.Project,
				ProjectID:     req.ProjectID,
				Due:           req.Due,
				Priority:      req.Priority,
				Scope:         req.Scope,
				Horizon:       req.Horizon,
				RoadmapNodeID: req.RoadmapNodeID,
				ExecutionType: "recurring",
			}
			if err := txStore.Tasks().Create(ctx, t); err != nil {
				return err
			}
			rule := recurrenceConfigToRule(t.ID, req.Recurrence)
			if err := txStore.Recurrence().UpsertRule(ctx, rule); err != nil {
				return err
			}
			task = t
			return nil
		})
		if err != nil {
			return nil, err
		}
		return store.Tasks().GetByID(ctx, task.ID)
	}

	// Single task
	task := &model.Task{
		Title:         req.Title,
		Content:       req.Content,
		Project:       req.Project,
		ProjectID:     req.ProjectID,
		Due:           req.Due,
		PlannedDate:   req.PlannedDate,
		Priority:      req.Priority,
		Scope:         req.Scope,
		Horizon:       req.Horizon,
		RoadmapNodeID: req.RoadmapNodeID,
		ExecutionType: "single",
	}
	if err := store.Tasks().Create(ctx, task); err != nil {
		return nil, err
	}
	return store.Tasks().GetByID(ctx, task.ID)
}

func UpdateTask(ctx context.Context, store storage.Store, id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	current, err := store.Tasks().GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	isRecurring := current.ExecutionType == "recurring"

	// Guard: cannot complete a recurring template
	if isRecurring {
		if (req.Done != nil && *req.Done == 1) || (req.Status != nil && *req.Status == "done") {
			return nil, &RecurringTaskError{
				Code:    "CANNOT_COMPLETE_RECURRING_TEMPLATE",
				Message: "cannot complete a recurring task template",
			}
		}
		// Guard: cannot switch recurring to single
		if req.ExecutionType != nil && *req.ExecutionType == "single" {
			return nil, &RecurringTaskError{
				Code:    "CANNOT_SWITCH_RECURRING_TO_SINGLE",
				Message: "cannot switch a recurring task to single",
			}
		}
	}

	// Switching single to recurring: validate recurrence and clear planned_date
	if req.ExecutionType != nil && *req.ExecutionType == "recurring" && !isRecurring {
		if req.Recurrence == nil {
			return nil, fmt.Errorf("recurrence config is required when switching to recurring")
		}
		if err := ValidateRecurrenceConfig(req.Recurrence); err != nil {
			return nil, err
		}
		empty := ""
		req.PlannedDate = &empty
	}

	// If recurrence config is provided, validate it
	if req.Recurrence != nil {
		if err := ValidateRecurrenceConfig(req.Recurrence); err != nil {
			return nil, err
		}
	}

	// Determine if we need a transaction for recurrence changes
	needsTransact := req.Recurrence != nil || (req.Enabled != nil && isRecurring) || (req.EndDate != nil && isRecurring) || (req.ExecutionType != nil && *req.ExecutionType == "recurring" && !isRecurring)

	if needsTransact {
		err = store.Transact(ctx, func(txStore storage.Store) error {
			if _, err := txStore.Tasks().Update(ctx, id, req); err != nil {
				return err
			}
			// Upsert recurrence rule if config provided or switching to recurring
			if req.Recurrence != nil {
				rule := recurrenceConfigToRule(id, req.Recurrence)
				if err := txStore.Recurrence().UpsertRule(ctx, rule); err != nil {
					return err
				}
			}
			// Update enabled state on existing rule
			if req.Enabled != nil && isRecurring {
				rule, err := txStore.Recurrence().GetRule(ctx, id)
				if err != nil {
					return err
				}
				if rule != nil {
					rule.Enabled = *req.Enabled
					if err := txStore.Recurrence().UpsertRule(ctx, rule); err != nil {
						return err
					}
				}
			}
			// Update end_date on existing rule
			if req.EndDate != nil && isRecurring {
				rule, err := txStore.Recurrence().GetRule(ctx, id)
				if err != nil {
					return err
				}
				if rule != nil {
					rule.EndDate = req.EndDate
					if err := txStore.Recurrence().UpsertRule(ctx, rule); err != nil {
						return err
					}
				}
			}
			return nil
		})
	} else {
		_, err = store.Tasks().Update(ctx, id, req)
	}
	if err != nil {
		return nil, err
	}
	return store.Tasks().GetByID(ctx, id)
}

func DeleteTask(ctx context.Context, store storage.Store, id string) error {
	return store.Tasks().Delete(ctx, id)
}

// recurrenceConfigToRule converts a RecurrenceConfig to a RecurrenceRule with defaults.
func recurrenceConfigToRule(taskID string, rc *model.RecurrenceConfig) *model.RecurrenceRule {
	interval := rc.Interval
	if interval == 0 {
		interval = 1
	}
	tz := rc.Timezone
	if tz == "" {
		tz = defaultTimezone()
	}
	return &model.RecurrenceRule{
		TaskID:    taskID,
		StartDate: rc.StartDate,
		EndDate:   rc.EndDate,
		Frequency: rc.Frequency,
		Interval:  interval,
		Weekdays:  rc.Weekdays,
		MonthDays: rc.MonthDays,
		Timezone:  tz,
		Enabled:   true,
	}
}
