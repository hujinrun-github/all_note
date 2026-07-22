package taskapp

import "context"

// ModelVersion is the durable workspace task-domain routing decision. It is
// intentionally smaller than the migration state machine: routers may select
// a handler only after a selector has proved one of these stable models.
type ModelVersion string

const (
	ModelLegacy ModelVersion = "legacy"
	ModelV2     ModelVersion = "v2"
)

// ModelSelector loads the authenticated workspace's durable model decision.
// Implementations must return an error for transitional, inconsistent, or
// unavailable state; callers must never infer legacy from such an error.
type ModelSelector interface {
	SelectTaskDomainModel(context.Context, string) (ModelVersion, error)
}
