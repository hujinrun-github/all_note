package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetTasks(project, status, scope, horizon, projectID, plannedDate string, page, pageSize int) ([]model.Task, int, error) {
	return repository.GetTasks(project, status, scope, horizon, projectID, plannedDate, page, pageSize)
}

func GetTaskProjects() ([]string, error) {
	return repository.GetTaskProjects()
}

func ListTaskProjects() ([]model.TaskProject, error) {
	return repository.ListTaskProjects()
}

func CreateTaskProject(req *model.CreateTaskProjectRequest) (*model.TaskProject, error) {
	return repository.CreateTaskProject(req)
}

func UpdateTaskProject(id string, req *model.UpdateTaskProjectRequest) (*model.TaskProject, error) {
	return repository.UpdateTaskProject(id, req)
}

func CreateTask(req *model.CreateTaskRequest) (*model.Task, error) {
	task := &model.Task{
		Title:         req.Title,
		Project:       req.Project,
		ProjectID:     req.ProjectID,
		Due:           req.Due,
		PlannedDate:   req.PlannedDate,
		Priority:      req.Priority,
		Scope:         req.Scope,
		Horizon:       req.Horizon,
		RoadmapNodeID: req.RoadmapNodeID,
	}
	if err := repository.CreateTask(task); err != nil {
		return nil, err
	}
	return repository.GetTaskByID(task.ID)
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	return repository.UpdateTask(id, req)
}

func DeleteTask(id string) error {
	return repository.DeleteTask(id)
}
