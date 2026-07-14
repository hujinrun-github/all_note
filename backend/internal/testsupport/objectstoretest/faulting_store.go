package objectstoretest

import (
	"context"
	"io"

	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/testsupport"
)

const (
	PutBefore    = "objectstore.put.before"
	PutAfter     = "objectstore.put.after"
	GetBefore    = "objectstore.get.before"
	GetAfter     = "objectstore.get.after"
	RemoveBefore = "objectstore.remove.before"
	RemoveAfter  = "objectstore.remove.after"
)

type FaultingStore struct {
	Base   objectstore.Store
	Faults testsupport.FaultInjector
}

func (s FaultingStore) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	if err := s.inject(PutBefore); err != nil {
		return err
	}
	if err := s.Base.Put(ctx, key, reader, size, contentType); err != nil {
		return err
	}
	return s.inject(PutAfter)
}

func (s FaultingStore) Get(ctx context.Context, key string) (*objectstore.Object, error) {
	if err := s.inject(GetBefore); err != nil {
		return nil, err
	}
	object, err := s.Base.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := s.inject(GetAfter); err != nil {
		_ = object.Body.Close()
		return nil, err
	}
	return object, nil
}

func (s FaultingStore) Remove(ctx context.Context, key string) error {
	if err := s.inject(RemoveBefore); err != nil {
		return err
	}
	if err := s.Base.Remove(ctx, key); err != nil {
		return err
	}
	return s.inject(RemoveAfter)
}

func (s FaultingStore) inject(point string) error {
	if s.Faults == nil {
		return nil
	}
	return s.Faults.Inject(point)
}
