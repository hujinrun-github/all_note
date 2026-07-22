package taskdomain

import (
	"errors"
)

type ErrorCode string

const (
	ErrorCodeInvalidProject            ErrorCode = "invalid_project"
	ErrorCodeInvalidSystemProjectSet   ErrorCode = "invalid_system_project_set"
	ErrorCodeSystemProjectImmutable    ErrorCode = "system_project_immutable"
	ErrorCodeProjectHasOpenOccurrences ErrorCode = "project_has_open_occurrences"
	ErrorCodeMultipleCurrentRoadmaps   ErrorCode = "multiple_current_roadmaps"
	ErrorCodeInvalidTaskTransition     ErrorCode = "invalid_task_transition"
	ErrorCodeLifecyclePatchForbidden   ErrorCode = "lifecycle_patch_forbidden"
)

type domainError struct {
	code ErrorCode
}

func (e *domainError) Error() string {
	return string(e.code)
}

func (e *domainError) Code() ErrorCode {
	return e.code
}

var (
	ErrInvalidProject            = &domainError{code: ErrorCodeInvalidProject}
	ErrInvalidSystemProjectSet   = &domainError{code: ErrorCodeInvalidSystemProjectSet}
	ErrSystemProjectImmutable    = &domainError{code: ErrorCodeSystemProjectImmutable}
	ErrProjectHasOpenOccurrences = &domainError{code: ErrorCodeProjectHasOpenOccurrences}
	ErrMultipleCurrentRoadmaps   = &domainError{code: ErrorCodeMultipleCurrentRoadmaps}
	ErrInvalidTaskTransition     = &domainError{code: ErrorCodeInvalidTaskTransition}
	ErrLifecyclePatchForbidden   = &domainError{code: ErrorCodeLifecyclePatchForbidden}
)

// ErrorCodeOf exposes a stable transport-facing code without coupling callers
// to domain state values or error messages.
func ErrorCodeOf(err error) ErrorCode {
	var coded interface{ Code() ErrorCode }
	if errors.As(err, &coded) {
		return coded.Code()
	}
	return ""
}
