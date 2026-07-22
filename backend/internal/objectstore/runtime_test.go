package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
)

type runtimeObjectSource struct {
	binding airuntime.Binding
	profile airuntime.EndpointProfile
}

func (s runtimeObjectSource) LoadBinding(context.Context, string, string) (airuntime.Binding, error) {
	return s.binding, nil
}
func (s runtimeObjectSource) LoadEndpointProfile(context.Context, string, string, string) (airuntime.EndpointProfile, error) {
	return s.profile, nil
}

type runtimeObjectFactory struct {
	builds int
	store  *memoryObjectStore
}

func (f *runtimeObjectFactory) Build(context.Context, airuntime.EndpointProfile) (Store, error) {
	f.builds++
	return f.store, nil
}

type memoryObjectStore struct{ value []byte }

func (s *memoryObjectStore) Put(_ context.Context, _ string, reader io.Reader, _ int64, _ string) error {
	s.value, _ = io.ReadAll(reader)
	return nil
}
func (s *memoryObjectStore) Get(context.Context, string) (*Object, error) {
	return &Object{Body: io.NopCloser(bytes.NewReader(s.value)), Size: int64(len(s.value))}, nil
}
func (*memoryObjectStore) Remove(context.Context, string) error { return nil }

func TestRuntimeStoreResolvesWorkspaceBindingAndCachesProfileVersion(t *testing.T) {
	factory := &runtimeObjectFactory{store: &memoryObjectStore{}}
	runtimeStore, _ := NewRuntimeStore(runtimeObjectSource{
		binding: airuntime.Binding{Kind: "object_s3", Mode: "custom", EndpointID: "objects"},
		profile: airuntime.EndpointProfile{EndpointID: "objects", Kind: "object_s3", ProfileVersionID: "v1", Provider: "minio"},
	}, factory)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "w1")
	if err := runtimeStore.Put(ctx, "one", bytes.NewBufferString("hello"), 5, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeStore.Get(ctx, "one"); err != nil || factory.builds != 1 {
		t.Fatalf("get err=%v builds=%d", err, factory.builds)
	}
}

func TestRuntimeStoreFailsClosedWithoutConcreteBinding(t *testing.T) {
	runtimeStore, _ := NewRuntimeStore(runtimeObjectSource{binding: airuntime.Binding{Kind: "object_s3", Mode: "disabled"}}, &runtimeObjectFactory{store: &memoryObjectStore{}})
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "w1")
	if _, err := runtimeStore.Get(ctx, "one"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error=%v", err)
	}
}
