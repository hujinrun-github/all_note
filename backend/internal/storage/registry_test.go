package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type fakeProvider struct {
	driver    Driver
	validate  func(Config) error
	open      func(context.Context, Config) (Store, error)
	migrated  bool
	validated bool
	opened    bool
}

func (p *fakeProvider) Driver() Driver { return p.driver }

func (p *fakeProvider) Validate(cfg Config) error {
	p.validated = true
	if p.validate != nil {
		return p.validate(cfg)
	}
	return nil
}

func (p *fakeProvider) Open(ctx context.Context, cfg Config) (Store, error) {
	p.opened = true
	if p.open != nil {
		return p.open(ctx, cfg)
	}
	return &fakeStore{}, nil
}

func (p *fakeProvider) Migrate(ctx context.Context, cfg Config) error {
	p.migrated = true
	return nil
}

type fakeStore struct{}

func (s *fakeStore) Close() error                 { return nil }
func (s *fakeStore) Health(context.Context) error { return nil }
func (s *fakeStore) Capabilities() Capabilities   { return Capabilities{} }
func (s *fakeStore) Transact(context.Context, func(Store) error) error {
	return errors.New("not implemented in fake")
}
func (s *fakeStore) Folders() FolderRepository   { return nil }
func (s *fakeStore) Notes() NoteRepository       { return nil }
func (s *fakeStore) Tasks() TaskRepository       { return nil }
func (s *fakeStore) Events() EventRepository     { return nil }
func (s *fakeStore) Inbox() InboxRepository      { return nil }
func (s *fakeStore) Roadmaps() RoadmapRepository { return nil }
func (s *fakeStore) Sync() SyncRepository        { return nil }
func (s *fakeStore) Search() SearchRepository    { return nil }

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&fakeProvider{driver: DriverPostgres}); err != nil {
		t.Fatalf("register first provider: %v", err)
	}
	if err := registry.Register(&fakeProvider{driver: DriverPostgres}); err == nil {
		t.Fatal("expected duplicate provider registration to fail")
	}
}

func TestRegistryRejectsUnknownDriver(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Open(context.Background(), Config{Driver: DriverPostgres}); err == nil {
		t.Fatal("expected unknown provider to fail")
	}
}

func TestRegistryValidatesBeforeOpen(t *testing.T) {
	expectedErr := errors.New("invalid config")
	provider := &fakeProvider{
		driver:   DriverPostgres,
		validate: func(Config) error { return expectedErr },
	}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	if _, err := registry.Open(context.Background(), Config{Driver: DriverPostgres}); !errors.Is(err, expectedErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if !provider.validated {
		t.Fatal("expected provider Validate to be called")
	}
	if provider.opened {
		t.Fatal("provider Open must not run after validation failure")
	}
}

func TestRegistrySupportsConcurrentRegisterAndOpen(t *testing.T) {
	registry := NewRegistry()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		driver := Driver(fmt.Sprintf("concurrent-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = registry.Register(&fakeProvider{driver: driver})
		}()
	}
	for i := 0; i < 50; i++ {
		driver := Driver(fmt.Sprintf("concurrent-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = registry.Open(ctx, Config{Driver: driver})
			}
		}()
	}
	wg.Wait()
}
