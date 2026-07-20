package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type fakeProvider struct {
	driver          Driver
	validate        func(Config) error
	open            func(context.Context, Config) (Store, error)
	migrated        bool
	validated       bool
	opened          bool
	openedControl   bool
	openedTenant    bool
	migratedControl bool
	migratedTenant  bool
	adoptedTenant   bool
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

func (p *fakeProvider) OpenControl(context.Context, Config) (Store, error) {
	p.openedControl = true
	return &fakeStore{}, nil
}

func (p *fakeProvider) MigrateControl(context.Context, Config) error {
	p.migratedControl = true
	return nil
}

func (p *fakeProvider) OpenTenant(context.Context, Config, string) (Store, error) {
	p.openedTenant = true
	return &fakeStore{}, nil
}

func (p *fakeProvider) MigrateTenant(context.Context, Config) error {
	p.migratedTenant = true
	return nil
}

func (p *fakeProvider) AdoptExistingTenant(context.Context, Config, AdoptManifest) error {
	p.adoptedTenant = true
	return nil
}

type fakeStore struct{}

func (s *fakeStore) Close() error                 { return nil }
func (s *fakeStore) Health(context.Context) error { return nil }
func (s *fakeStore) Capabilities() Capabilities   { return Capabilities{} }
func (s *fakeStore) Transact(context.Context, func(Store) error) error {
	return errors.New("not implemented in fake")
}
func (s *fakeStore) Folders() FolderRepository { return nil }
func (s *fakeStore) Notes() NoteRepository     { return nil }
func (s *fakeStore) Tasks() TaskRepository     { return nil }
func (s *fakeStore) Recurrence() RecurrenceRepository {
	return nil
}
func (s *fakeStore) Events() EventRepository      { return nil }
func (s *fakeStore) Calendar() CalendarRepository { return nil }
func (s *fakeStore) Inbox() InboxRepository       { return nil }
func (s *fakeStore) Roadmaps() RoadmapRepository  { return nil }
func (s *fakeStore) Sync() SyncRepository         { return nil }
func (s *fakeStore) Search() SearchRepository     { return nil }
func (s *fakeStore) Auth() AuthRepository         { return nil }

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

func TestRegistryRoutesRoleSpecificOperations(t *testing.T) {
	registry := NewRegistry()
	provider := &fakeProvider{driver: DriverPostgres}
	if err := registry.Register(provider); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	cfg := Config{Driver: DriverPostgres}
	ctx := context.Background()

	control, err := registry.OpenControl(ctx, cfg)
	if err != nil {
		t.Fatalf("open control: %v", err)
	}
	_ = control.Close()
	if err := registry.MigrateControl(ctx, cfg); err != nil {
		t.Fatalf("migrate control: %v", err)
	}
	tenant, err := registry.OpenTenant(ctx, cfg, "0001")
	if err != nil {
		t.Fatalf("open tenant: %v", err)
	}
	_ = tenant.Close()
	if err := registry.MigrateTenant(ctx, cfg); err != nil {
		t.Fatalf("migrate tenant: %v", err)
	}
	if err := registry.AdoptExistingTenant(ctx, cfg, AdoptManifest{ID: "legacy"}); err != nil {
		t.Fatalf("adopt tenant: %v", err)
	}

	if !provider.openedControl || !provider.migratedControl || !provider.openedTenant || !provider.migratedTenant || !provider.adoptedTenant {
		t.Fatalf("role operations were not routed: %+v", provider)
	}
	if provider.opened || provider.migrated {
		t.Fatal("role operations must not call legacy Open/Migrate")
	}
}
