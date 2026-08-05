package storage

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/model"
)

var (
	ErrContentImportStorage      = errors.New("content import storage is unavailable")
	ErrNoContentImport           = errors.New("no content import is eligible")
	ErrContentImportLeaseLost    = errors.New("content import lease was lost")
	ErrContentImportNotRetryable = errors.New("content import is not retryable")
	ErrContentImportNotDeletable = errors.New("content import is not deletable")
)

type ContentImportStore interface {
	ContentImports() ContentImportRepository
}

func ContentImportRepositoryFrom(store Store) (ContentImportRepository, error) {
	contentStore, ok := store.(ContentImportStore)
	if !ok || contentStore.ContentImports() == nil {
		return nil, ErrContentImportStorage
	}
	return contentStore.ContentImports(), nil
}

type ContentImportRepository interface {
	CreateOrGet(context.Context, model.CreateContentImport) (*model.ContentImport, error)
	List(context.Context, model.ContentImportFilter) ([]model.ContentImport, int, error)
	Get(context.Context, string) (*model.ContentImport, error)
	GetByResultNoteID(context.Context, string) (*model.ContentImport, error)
	Cancel(context.Context, string, int64) (*model.ContentImport, error)
	Retry(context.Context, string, int64) (*model.ContentImport, error)
	Delete(context.Context, string) error

	ClaimNext(context.Context, model.ClaimContentImport) (*model.ContentImportLease, error)
	UpdateResolved(context.Context, model.UpdateContentImportResolved) (*model.ContentImport, error)
	UpdateStage(context.Context, model.UpdateContentImportStage) (*model.ContentImport, error)
	Heartbeat(context.Context, model.HeartbeatContentImport) error
	PutArtifact(context.Context, model.ContentImportArtifact) error
	GetArtifact(context.Context, string, string) (*model.ContentImportArtifact, error)
	Fail(context.Context, model.FailContentImport) (*model.ContentImport, error)
	Complete(context.Context, model.CompleteContentImport) (*model.ContentImport, error)
}
