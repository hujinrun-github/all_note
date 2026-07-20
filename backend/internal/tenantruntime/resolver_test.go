package tenantruntime

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct {
	version      Version
	loadVersion  int
	loadSnapshot int
	snapshotErr  error
}

func (f *fakeSource) LoadVersion(context.Context, string) (Version, error) {
	f.loadVersion++
	return f.version, nil
}

func (f *fakeSource) LoadSnapshot(_ context.Context, version Version) (Snapshot, error) {
	f.loadSnapshot++
	if f.snapshotErr != nil {
		return Snapshot{}, f.snapshotErr
	}
	return Snapshot{Version: version, DatabaseEndpointID: "db"}, nil
}

type fakeResource struct{ closed int }

func (r *fakeResource) Close() error { r.closed++; return nil }

type fakeFactory struct {
	builds int
	items  []*fakeResource
}

func (f *fakeFactory) Build(context.Context, Snapshot) (Resource, error) {
	f.builds++
	item := &fakeResource{}
	f.items = append(f.items, item)
	return item, nil
}

func TestResolverChecksPersistentVersionOnEveryRequest(t *testing.T) {
	source := &fakeSource{version: Version{WorkspaceID: "w1", Mode: "active", Epoch: 1, BindingRevision: 1}}
	factory := &fakeFactory{}
	resolver, _ := NewResolver(source, factory)
	defer resolver.Close()
	first, err := resolver.Resolve(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if source.loadVersion != 2 || source.loadSnapshot != 1 || factory.builds != 1 || first.Resource != second.Resource {
		t.Fatalf("unexpected cache behavior versions=%d snapshots=%d builds=%d", source.loadVersion, source.loadSnapshot, factory.builds)
	}
	source.version.BindingRevision = 2
	third, err := resolver.Resolve(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if factory.builds != 2 || third.Resource == first.Resource || factory.items[0].closed != 1 {
		t.Fatal("revision change did not replace and close cached resource")
	}
}

func TestResolverFailsClosedForModeAndSnapshotErrors(t *testing.T) {
	source := &fakeSource{version: Version{WorkspaceID: "w1", Mode: "draining", Epoch: 2, BindingRevision: 1}}
	resolver, _ := NewResolver(source, &fakeFactory{})
	if _, err := resolver.Resolve(context.Background(), "w1"); !errors.Is(err, ErrRuntimeNotActive) {
		t.Fatalf("draining error=%v", err)
	}
	source.version.Mode = "active"
	source.snapshotErr = errors.New("control unavailable")
	if _, err := resolver.Resolve(context.Background(), "w1"); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("snapshot error=%v", err)
	}
}

func TestResolverRejectsInconsistentSnapshotVersion(t *testing.T) {
	source := &inconsistentSource{version: Version{WorkspaceID: "w1", Mode: "active", Epoch: 1, BindingRevision: 1}}
	resolver, _ := NewResolver(source, &fakeFactory{})
	if _, err := resolver.Resolve(context.Background(), "w1"); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("inconsistent snapshot error=%v", err)
	}
}

type inconsistentSource struct{ version Version }

func (s *inconsistentSource) LoadVersion(context.Context, string) (Version, error) {
	return s.version, nil
}
func (s *inconsistentSource) LoadSnapshot(_ context.Context, version Version) (Snapshot, error) {
	version.Epoch++
	return Snapshot{Version: version}, nil
}
