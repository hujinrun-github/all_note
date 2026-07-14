package objectstore

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"sync"
)

type memoryObject struct {
	data        []byte
	contentType string
	etag        string
}

type MemoryStore struct {
	mu      sync.RWMutex
	objects map[string]memoryObject
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string]memoryObject)}
}

func (s *MemoryStore) Put(_ context.Context, key string, reader io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(io.LimitReader(reader, size+1))
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrUnexpectedEOF
	}
	digest := md5.Sum(data)
	s.mu.Lock()
	s.objects[key] = memoryObject{data: data, contentType: contentType, etag: hex.EncodeToString(digest[:])}
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Get(_ context.Context, key string) (*Object, error) {
	s.mu.RLock()
	stored, ok := s.objects[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	data := append([]byte(nil), stored.data...)
	return &Object{
		Body:        io.NopCloser(bytes.NewReader(data)),
		Size:        int64(len(data)),
		ContentType: stored.contentType,
		ETag:        stored.etag,
	}, nil
}

func (s *MemoryStore) Remove(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[key]; !ok {
		return ErrNotFound
	}
	delete(s.objects, key)
	return nil
}
