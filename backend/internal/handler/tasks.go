package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetTasks(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := getPagination(c)
		project := c.Query("project")
		status := c.DefaultQuery("status", "all")
		scope := c.Query("scope")
		horizon := c.Query("horizon")
		projectID := c.Query("project_id")
		plannedDate := c.Query("planned_date")
		plannedFrom := c.Query("planned_from")
		plannedTo := c.Query("planned_to")
		executionType := c.Query("execution_type")

		tasks, total, err := service.GetTasks(c.Request.Context(), store, project, status, scope, horizon, projectID, plannedDate, plannedFrom, plannedTo, executionType, page, pageSize)
		if err != nil {
			internalError(c, "failed to get tasks")
			return
		}
		successWithPagination(c, gin.H{"tasks": tasks}, page, pageSize, total)
	}
}

func GetTaskProjects(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projects, err := service.GetTaskProjects(c.Request.Context(), store)
		if err != nil {
			internalError(c, "failed to get task projects")
			return
		}
		success(c, gin.H{"projects": projects})
	}
}

func ListTaskProjects(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		projects, err := service.ListTaskProjects(c.Request.Context(), store)
		if err != nil {
			internalError(c, "failed to list task projects")
			return
		}
		success(c, gin.H{"projects": projects})
	}
}

func CreateTaskProject(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateTaskProjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "project name is required")
			return
		}
		project, err := service.CreateTaskProject(c.Request.Context(), store, &req)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		created(c, gin.H{"project": project})
	}
}

func UpdateTaskProject(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateTaskProjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		project, err := service.UpdateTaskProject(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			notFound(c, "project not found")
			return
		}
		success(c, gin.H{"project": project})
	}
}

func DeleteTaskProject(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteTaskProject(c.Request.Context(), store, c.Param("id")); err != nil {
			badRequest(c, err.Error())
			return
		}
		noContent(c)
	}
}

func CreateTask(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateTaskRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "title is required")
			return
		}
		task, err := service.CreateTask(c.Request.Context(), store, &req)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		created(c, gin.H{"task": task})
	}
}

func UpdateTask(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateTaskRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		task, err := service.UpdateTask(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			if rte, ok := err.(*service.RecurringTaskError); ok {
				conflict(c, rte.Code, rte.Message)
				return
			}
			notFound(c, "task not found")
			return
		}
		success(c, gin.H{"task": task})
	}
}

func DeleteTask(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteTask(c.Request.Context(), store, c.Param("id")); err != nil {
			internalError(c, "failed to delete task")
			return
		}
		noContent(c)
	}
}

func CompleteOccurrence(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("id")
		date := c.Param("date")
		ctx := c.Request.Context()

		if err := validateOccurrenceAction(ctx, store, taskID, date); err != nil {
			badRequest(c, err.Error())
			return
		}

		occurrence, err := store.Recurrence().CompleteOccurrence(ctx, taskID, date, time.Now().Unix())
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"occurrence": occurrence})
	}
}

func ReopenOccurrence(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("id")
		date := c.Param("date")
		ctx := c.Request.Context()

		if err := validateOccurrenceAction(ctx, store, taskID, date); err != nil {
			badRequest(c, err.Error())
			return
		}

		occurrence, err := store.Recurrence().ReopenOccurrence(ctx, taskID, date)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"occurrence": occurrence})
	}
}

func SkipOccurrence(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		taskID := c.Param("id")
		date := c.Param("date")
		ctx := c.Request.Context()

		if err := validateOccurrenceAction(ctx, store, taskID, date); err != nil {
			badRequest(c, err.Error())
			return
		}

		occurrence, err := store.Recurrence().SkipOccurrence(ctx, taskID, date)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"occurrence": occurrence})
	}
}

func GetTaskOccurrences(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		from := c.DefaultQuery("from", "")
		to := c.DefaultQuery("to", "")
		if from == "" || to == "" {
			badRequest(c, "from and to query parameters are required")
			return
		}

		occurrences, err := store.Recurrence().ListOccurrences(c.Request.Context(), from, to)
		if err != nil {
			internalError(c, err.Error())
			return
		}
		success(c, gin.H{"occurrences": occurrences})
	}
}

func validateOccurrenceAction(ctx context.Context, store storage.Store, taskID, date string) error {
	task, err := store.Tasks().GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("task not found")
	}
	if task.ExecutionType != "recurring" {
		return fmt.Errorf("task is not a recurring task")
	}

	rule, err := store.Recurrence().GetRule(ctx, taskID)
	if err != nil {
		return fmt.Errorf("recurrence rule not found")
	}

	occurrences := service.ExpandRuleOccurrences(rule, date, date)
	if len(occurrences) == 0 {
		return fmt.Errorf("date is not an expected occurrence")
	}

	return nil
}
