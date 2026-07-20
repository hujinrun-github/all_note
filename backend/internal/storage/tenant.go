package storage

import (
	"context"
	"errors"
)

var (
	ErrTenantWorkspaceMissing = errors.New("tenant workspace anchor is missing")
	ErrTenantWorkspaceFenced  = errors.New("tenant workspace is not writable")
	ErrTenantEpochMismatch    = errors.New("tenant runtime epoch mismatch")
	ErrTenantWriteTxClosed    = errors.New("tenant write transaction is closed")
)

type TenantOutboxEvent struct {
	ID                string
	Topic             string
	AggregateID       string
	AggregateRevision int64
	PayloadJSON       string
}

type TenantWriteTx interface {
	EnqueueOutbox(context.Context, TenantOutboxEvent) error
}

type TenantFencedWriter interface {
	BeginFencedWrite(context.Context, string, int64, func(TenantWriteTx) error) error
}

type TenantMigrationFencer interface {
	FenceWorkspace(context.Context, string, int64, string) (int64, error)
	ActivateWorkspace(context.Context, string, int64, string) error
}
