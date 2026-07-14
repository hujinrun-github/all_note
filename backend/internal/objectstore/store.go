package objectstore

import (
	"context"
	"errors"
	"io"
)

var (
	ErrUnavailable = errors.New("object storage is not configured")
	ErrNotFound    = errors.New("object not found")
)

type Object struct {
	Body        io.ReadCloser
	Size        int64
	ContentType string
	ETag        string
}

type Store interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (*Object, error)
	Remove(context.Context, string) error
}

type UnavailableStore struct{}

func (UnavailableStore) Put(context.Context, string, io.Reader, int64, string) error {
	return ErrUnavailable
}

func (UnavailableStore) Get(context.Context, string) (*Object, error) {
	return nil, ErrUnavailable
}

func (UnavailableStore) Remove(context.Context, string) error {
	return ErrUnavailable
}
