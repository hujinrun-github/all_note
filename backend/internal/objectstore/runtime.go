package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
)

type RuntimeSource interface {
	LoadBinding(context.Context, string, string) (airuntime.Binding, error)
	LoadEndpointProfile(context.Context, string, string, string) (airuntime.EndpointProfile, error)
}

type RuntimeFactory interface {
	Build(context.Context, airuntime.EndpointProfile) (Store, error)
}

type RuntimeStore struct {
	source  RuntimeSource
	factory RuntimeFactory
	mu      sync.Mutex
	cache   map[string]Store
}

func NewRuntimeStore(source RuntimeSource, factory RuntimeFactory) (*RuntimeStore, error) {
	if source == nil || factory == nil {
		return nil, errors.New("object runtime source and factory are required")
	}
	return &RuntimeStore{source: source, factory: factory, cache: make(map[string]Store)}, nil
}

func (s *RuntimeStore) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	store, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	return store.Put(ctx, key, reader, size, contentType)
}

func (s *RuntimeStore) Get(ctx context.Context, key string) (*Object, error) {
	store, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return store.Get(ctx, key)
}

func (s *RuntimeStore) Remove(ctx context.Context, key string) error {
	store, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	return store.Remove(ctx, key)
}

func (s *RuntimeStore) resolve(ctx context.Context) (Store, error) {
	workspaceID, err := auth.WorkspaceIDFromContext(ctx)
	if err != nil {
		return nil, ErrUnavailable
	}
	binding, err := s.source.LoadBinding(ctx, workspaceID, "object_s3")
	if err != nil || (binding.Mode != "default" && binding.Mode != "custom") || binding.EndpointID == "" {
		return nil, ErrUnavailable
	}
	profile, err := s.source.LoadEndpointProfile(ctx, workspaceID, "object_s3", binding.EndpointID)
	if err != nil {
		return nil, ErrUnavailable
	}
	cacheKey := workspaceID + ":" + profile.ProfileVersionID
	s.mu.Lock()
	cached := s.cache[cacheKey]
	s.mu.Unlock()
	if cached != nil {
		clear(profile.Secret)
		return cached, nil
	}
	built, err := s.factory.Build(ctx, profile)
	clear(profile.Secret)
	if err != nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	if cached = s.cache[cacheKey]; cached == nil {
		s.cache[cacheKey] = built
		cached = built
	}
	s.mu.Unlock()
	return cached, nil
}

type MinIORuntimeFactory struct{ transport http.RoundTripper }

func NewMinIORuntimeFactory(client *http.Client) (*MinIORuntimeFactory, error) {
	if client == nil || client.Transport == nil {
		return nil, errors.New("controlled object HTTP client is required")
	}
	return &MinIORuntimeFactory{transport: client.Transport}, nil
}

func (f *MinIORuntimeFactory) Build(ctx context.Context, profile airuntime.EndpointProfile) (Store, error) {
	if profile.Provider == "unavailable" {
		return nil, ErrUnavailable
	}
	if profile.Provider != "minio" && profile.Provider != "s3" {
		return nil, errors.New("unsupported object storage provider")
	}
	var settings struct {
		Endpoint string `json:"endpoint"`
		Bucket   string `json:"bucket"`
		Region   string `json:"region"`
	}
	var credentials struct {
		AccessKey string `json:"access_key"`
		SecretKey string `json:"secret_key"`
	}
	if json.Unmarshal([]byte(profile.ConfigJSON), &settings) != nil || json.Unmarshal(profile.Secret, &credentials) != nil {
		return nil, errors.New("invalid object storage profile")
	}
	parsed, err := url.Parse(strings.TrimSpace(settings.Endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimSpace(settings.Bucket) == "" || credentials.AccessKey == "" || credentials.SecretKey == "" {
		return nil, errors.New("incomplete object storage profile")
	}
	return NewMinIORuntimeStore(ctx, config.MinIOConfig{
		Endpoint: parsed.Host, AccessKey: credentials.AccessKey, SecretKey: credentials.SecretKey,
		Bucket: settings.Bucket, Region: settings.Region, UseSSL: parsed.Scheme == "https",
	}, f.transport)
}

var _ Store = (*RuntimeStore)(nil)
