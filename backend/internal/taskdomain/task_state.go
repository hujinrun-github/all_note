package taskdomain

func PublishTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecycleDraft, TaskLifecycleActive)
}

func PauseTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecycleActive, TaskLifecyclePaused)
}

func ResumeTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecyclePaused, TaskLifecycleActive)
}

func CompleteTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecycleActive, TaskLifecycleCompleted)
}

func CancelTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	if current != TaskLifecycleActive && current != TaskLifecyclePaused {
		return current, ErrInvalidTaskTransition
	}
	return TaskLifecycleCancelled, nil
}

func ReopenTaskFromOccurrence(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecycleCompleted, TaskLifecycleActive)
}

func RestoreTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	return transitionTask(current, TaskLifecycleCancelled, TaskLifecycleActive)
}

func ArchiveTask(current TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	if current != TaskLifecycleCompleted && current != TaskLifecycleCancelled {
		return current, ErrInvalidTaskTransition
	}
	return TaskLifecycleArchived, nil
}

func PatchTaskDefinition(task TaskDefinition, patch TaskPatch) (TaskDefinition, error) {
	if patch.LifecycleStatus != nil {
		return task, ErrLifecyclePatchForbidden
	}
	task.Title = patch.Title
	task.Description = patch.Description
	task.Priority = patch.Priority
	return task, nil
}

func transitionTask(current, expected, next TaskLifecycleStatus) (TaskLifecycleStatus, error) {
	if current != expected {
		return current, ErrInvalidTaskTransition
	}
	return next, nil
}
