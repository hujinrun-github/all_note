package service

import (
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
)

func GetTasks(project, status, scope string, page, pageSize int) ([]model.Task, int, error) {
	return repository.GetTasks(project, status, scope, page, pageSize)
}

func CreateTask(req *model.CreateTaskRequest) (*model.Task, error) {
	task := &model.Task{
		Title:    req.Title,
		Project:  req.Project,
		Due:      req.Due,
		Priority: req.Priority,
		Scope:    req.Scope,
	}
	if err := repository.CreateTask(task); err != nil {
		return nil, err
	}
	return task, nil
}

func UpdateTask(id string, req *model.UpdateTaskRequest) (*model.Task, error) {
	return repository.UpdateTask(id, req)
}

func DeleteTask(id string) error {
	return repository.DeleteTask(id)
}
