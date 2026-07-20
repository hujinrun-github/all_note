package tenantruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrRuntimeNotActive   = errors.New("tenant runtime is not active")
	ErrRuntimeUnavailable = errors.New("tenant runtime is unavailable")
)

type Version struct {
	WorkspaceID     string
	Mode            string
	Epoch           int64
	BindingRevision int64
}

type Snapshot struct {
	Version
	DatabaseEndpointID      string
	ObjectEndpointID        string
	ChatMode                string
	ChatEndpointID          string
	TranscriptionMode       string
	TranscriptionEndpointID string
}

type Source interface {
	LoadVersion(context.Context, string) (Version, error)
	LoadSnapshot(context.Context, Version) (Snapshot, error)
}

type Resource interface {
	Close() error
}

type Factory interface {
	Build(context.Context, Snapshot) (Resource, error)
}

type Runtime struct {
	Snapshot Snapshot
	Resource Resource
}

type cacheEntry struct {
	version  Version
	snapshot Snapshot
	resource Resource
}

type Resolver struct {
	source  Source
	factory Factory
	mu      sync.Mutex
	cache   map[string]cacheEntry
	gates   map[string]*sync.Mutex
}

func NewResolver(source Source, factory Factory) (*Resolver, error) {
	if source == nil || factory == nil {
		return nil, errors.New("runtime source and factory are required")
	}
	return &Resolver{source: source, factory: factory, cache: make(map[string]cacheEntry), gates: make(map[string]*sync.Mutex)}, nil
}

func (r *Resolver) Resolve(ctx context.Context, workspaceID string) (Runtime, error) {
	version, err := r.source.LoadVersion(ctx, workspaceID)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: load persistent version: %v", ErrRuntimeUnavailable, err)
	}
	if version.WorkspaceID != workspaceID || version.Epoch < 1 || version.BindingRevision < 1 {
		return Runtime{}, fmt.Errorf("%w: invalid persistent version", ErrRuntimeUnavailable)
	}
	if version.Mode != "active" {
		return Runtime{}, fmt.Errorf("%w: mode=%s", ErrRuntimeNotActive, version.Mode)
	}
	gate := r.workspaceGate(workspaceID)
	gate.Lock()
	defer gate.Unlock()
	r.mu.Lock()
	if cached, ok := r.cache[workspaceID]; ok && sameVersion(cached.version, version) {
		r.mu.Unlock()
		return Runtime{Snapshot: cached.snapshot, Resource: cached.resource}, nil
	}
	r.mu.Unlock()
	snapshot, err := r.source.LoadSnapshot(ctx, version)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: load snapshot: %v", ErrRuntimeUnavailable, err)
	}
	if !sameVersion(snapshot.Version, version) {
		return Runtime{}, fmt.Errorf("%w: snapshot version changed during resolve", ErrRuntimeUnavailable)
	}
	resource, err := r.factory.Build(ctx, snapshot)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: build runtime: %v", ErrRuntimeUnavailable, err)
	}
	r.mu.Lock()
	previous, hadPrevious := r.cache[workspaceID]
	r.cache[workspaceID] = cacheEntry{version: version, snapshot: snapshot, resource: resource}
	r.mu.Unlock()
	if hadPrevious {
		_ = previous.resource.Close()
	}
	return Runtime{Snapshot: snapshot, Resource: resource}, nil
}

func (r *Resolver) Invalidate(workspaceID string) {
	gate := r.workspaceGate(workspaceID)
	gate.Lock()
	defer gate.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[workspaceID]; ok {
		_ = cached.resource.Close()
		delete(r.cache, workspaceID)
	}
}

func (r *Resolver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var first error
	for workspaceID, cached := range r.cache {
		if err := cached.resource.Close(); err != nil && first == nil {
			first = err
		}
		delete(r.cache, workspaceID)
	}
	return first
}

func sameVersion(left, right Version) bool {
	return left.WorkspaceID == right.WorkspaceID && left.Mode == right.Mode && left.Epoch == right.Epoch && left.BindingRevision == right.BindingRevision
}

func (r *Resolver) workspaceGate(workspaceID string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	gate := r.gates[workspaceID]
	if gate == nil {
		gate = &sync.Mutex{}
		r.gates[workspaceID] = gate
	}
	return gate
}
