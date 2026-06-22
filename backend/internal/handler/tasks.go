package handler

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetTasks(c *gin.Context) {
	page, pageSize := getPagination(c)
	project := c.Query("project")
	status := c.DefaultQuery("status", "all")
	scope := c.Query("scope")
	horizon := c.Query("horizon")
	projectID := c.Query("project_id")
	plannedDate := c.Query("planned_date")
	executionType := c.Query("execution_type")

	store := repository.ActiveStore()
	tasks, total, err := service.GetTasks(c.Request.Context(), store, project, status, scope, horizon, projectID, plannedDate, executionType, page, pageSize)
	if err != nil {
		internalError(c, "failed to get tasks")
		return
	}
	successWithPagination(c, gin.H{"tasks": tasks}, page, pageSize, total)
}

func GetTaskProjects(c *gin.Context) {
	store := repository.ActiveStore()
	projects, err := service.GetTaskProjects(c.Request.Context(), store)
	if err != nil {
		internalError(c, "failed to get task projects")
		return
	}
	success(c, gin.H{"projects": projects})
}

func ListTaskProjects(c *gin.Context) {
	store := repository.ActiveStore()
	projects, err := service.ListTaskProjects(c.Request.Context(), store)
	if err != nil {
		internalError(c, "failed to list task projects")
		return
	}
	success(c, gin.H{"projects": projects})
}

func CreateTaskProject(c *gin.Context) {
	var req model.CreateTaskProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "project name is required")
		return
	}
	store := repository.ActiveStore()
	project, err := service.CreateTaskProject(c.Request.Context(), store, &req)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	created(c, gin.H{"project": project})
}

func UpdateTaskProject(c *gin.Context) {
	var req model.UpdateTaskProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	store := repository.ActiveStore()
	project, err := service.UpdateTaskProject(c.Request.Context(), store, c.Param("id"), &req)
	if err != nil {
		notFound(c, "project not found")
		return
	}
	success(c, gin.H{"project": project})
}

func DeleteTaskProject(c *gin.Context) {
	store := repository.ActiveStore()
	if err := service.DeleteTaskProject(c.Request.Context(), store, c.Param("id")); err != nil {
		badRequest(c, err.Error())
		return
	}
	noContent(c)
}

func CreateTask(c *gin.Context) {
	var req model.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title is required")
		return
	}
	store := repository.ActiveStore()
	task, err := service.CreateTask(c.Request.Context(), store, &req)
	if err != nil {
		internalError(c, err.Error())
		return
	}
	created(c, gin.H{"task": task})
}

func UpdateTask(c *gin.Context) {
	var req model.UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	store := repository.ActiveStore()
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

func DeleteTask(c *gin.Context) {
	store := repository.ActiveStore()
	if err := service.DeleteTask(c.Request.Context(), store, c.Param("id")); err != nil {
		internalError(c, "failed to delete task")
		return
	}
	noContent(c)
}

// CompleteOccurrence marks a recurring task occurrence as completed.
func CompleteOccurrence(c *gin.Context) {
	taskID := c.Param("id")
	date := c.Param("date")
	store := repository.ActiveStore()
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

// ReopenOccurrence reopens a completed or skipped occurrence.
func ReopenOccurrence(c *gin.Context) {
	taskID := c.Param("id")
	date := c.Param("date")
	store := repository.ActiveStore()
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

// SkipOccurrence marks a recurring task occurrence as skipped.
func SkipOccurrence(c *gin.Context) {
	taskID := c.Param("id")
	date := c.Param("date")
	store := repository.ActiveStore()
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

// GetTaskOccurrences returns occurrences for recurring tasks in a date range.
func GetTaskOccurrences(c *gin.Context) {
	from := c.DefaultQuery("from", "")
	to := c.DefaultQuery("to", "")
	if from == "" || to == "" {
		badRequest(c, "from and to query parameters are required")
		return
	}

	store := repository.ActiveStore()
	occurrences, err := store.Recurrence().ListOccurrences(c.Request.Context(), from, to)
	if err != nil {
		internalError(c, err.Error())
		return
	}
	success(c, gin.H{"occurrences": occurrences})
}

// validateOccurrenceAction checks that the task exists, is recurring, and the date is an expected occurrence.
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
