package storage

import (
	"context"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// TaskDomainRuntimeState is the narrow, provider-neutral state needed before
// a request may use the v2 task domain. Provider implementations load the
// domain state and tenant anchor together so callers cannot accidentally
// accept a split epoch.
type TaskDomainRuntimeState struct {
	WorkspaceID    string
	ModelVersion   string
	MigrationState string
	Epoch          int64
	AnchorState    string
}

// TaskDomainRuntimeStore is the only tenant-store capability exposed to the
// task runtime assembler. It deliberately does not expose Store.Transact or a
// write repository; writes are created separately by TenantFencedWriter.
type TaskDomainRuntimeStore interface {
	TaskDomainReader(string) taskdomain.TaskDomainReader
	LoadTaskDomainRuntimeState(context.Context, string) (TaskDomainRuntimeState, error)
}
