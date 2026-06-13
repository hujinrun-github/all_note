package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetTasks(c *gin.Context) {
	page, pageSize := getPagination(c)
	project := c.Query("project")
	status := c.DefaultQuery("status", "all")
	scope := c.Query("scope")
	horizon := c.Query("horizon")
	projectID := c.Query("project_id")
	plannedDate := c.Query("planned_date")

	tasks, total, err := service.GetTasks(project, status, scope, horizon, projectID, plannedDate, page, pageSize)
	if err != nil {
		internalError(c, "failed to get tasks")
		return
	}
	successWithPagination(c, gin.H{"tasks": tasks}, page, pageSize, total)
}

func GetTaskProjects(c *gin.Context) {
	projects, err := service.GetTaskProjects()
	if err != nil {
		internalError(c, "failed to get task projects")
		return
	}
	success(c, gin.H{"projects": projects})
}

func ListTaskProjects(c *gin.Context) {
	projects, err := service.ListTaskProjects()
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
	project, err := service.CreateTaskProject(&req)
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
	project, err := service.UpdateTaskProject(c.Param("id"), &req)
	if err != nil {
		notFound(c, "project not found")
		return
	}
	success(c, gin.H{"project": project})
}

func DeleteTaskProject(c *gin.Context) {
	if err := service.DeleteTaskProject(c.Param("id")); err != nil {
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
	task, err := service.CreateTask(&req)
	if err != nil {
		internalError(c, "failed to create task")
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
	task, err := service.UpdateTask(c.Param("id"), &req)
	if err != nil {
		notFound(c, "task not found")
		return
	}
	success(c, gin.H{"task": task})
}

func DeleteTask(c *gin.Context) {
	if err := service.DeleteTask(c.Param("id")); err != nil {
		internalError(c, "failed to delete task")
		return
	}
	noContent(c)
}
