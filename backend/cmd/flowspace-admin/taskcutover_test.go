package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
)

type taskCutoverHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (fn taskCutoverHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type staticTaskCutoverGate struct {
	err error
}

func (gate staticTaskCutoverGate) Preflight() error {
	return gate.err
}

func (gate staticTaskCutoverGate) CountOldWriterHeartbeats(context.Context, string) (int, error) {
	if gate.err != nil {
		return 1, nil
	}
	return 0, nil
}

func TestSingleInstanceOfflineGateRequiresBackendToRejectConnections(t *testing.T) {
	reachable := &singleInstanceOfflineGate{
		healthURL: backendHealthURL,
		client: taskCutoverHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != backendHealthURL {
				t.Fatalf("health URL = %s", request.URL)
			}
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader(`{"status":"starting"}`)),
			}, nil
		}),
	}
	if err := reachable.Preflight(); !errors.Is(err, errBackendStillReachable) {
		t.Fatalf("reachable backend preflight error = %v", err)
	}
	if count, err := reachable.CountOldWriterHeartbeats(context.Background(), "w1"); err != nil || count != 1 {
		t.Fatalf("reachable heartbeat count=%d error=%v", count, err)
	}

	offline := &singleInstanceOfflineGate{
		healthURL: backendHealthURL,
		client: taskCutoverHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		}),
	}
	if err := offline.Preflight(); err != nil {
		t.Fatalf("offline backend preflight: %v", err)
	}
	if count, err := offline.CountOldWriterHeartbeats(context.Background(), "w1"); err != nil || count != 0 {
		t.Fatalf("offline heartbeat count=%d error=%v", count, err)
	}
}

func TestCutoverTaskMigrationRejectsUnsafeDeploymentBeforeOpeningStores(t *testing.T) {
	cfg := adminTestRuntimeConfig()
	cfg.InstanceMode = config.InstanceModeMulti
	err := cutoverTaskMigration(context.Background(), cfg, nil, taskMigrationCutoverOptions{
		RoutingEnabled: true,
		OfflineGate:    staticTaskCutoverGate{},
	})
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_INSTANCE_MODE=single") {
		t.Fatalf("multi-instance error = %v", err)
	}

	cfg.InstanceMode = config.InstanceModeSingle
	err = cutoverTaskMigration(context.Background(), cfg, nil, taskMigrationCutoverOptions{
		RoutingEnabled: false,
		OfflineGate:    staticTaskCutoverGate{},
	})
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_ENABLE_TASK_DOMAIN_V2_ROUTING=true") {
		t.Fatalf("routing-disabled error = %v", err)
	}

	err = cutoverTaskMigration(context.Background(), cfg, nil, taskMigrationCutoverOptions{
		RoutingEnabled: true,
		OfflineGate:    staticTaskCutoverGate{err: errBackendStillReachable},
	})
	if err == nil || !errors.Is(err, errBackendStillReachable) {
		t.Fatalf("reachable-backend error = %v", err)
	}
}

func TestSynchronizeControlEpochAdvancesOnceAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	controlPath := filepath.Join(t.TempDir(), "control.db")
	controlConfig := storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: controlPath,
	}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateControl(ctx, controlConfig); err != nil {
		t.Fatal(err)
	}
	store, err := provider.OpenControl(ctx, controlConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.(storage.SQLStore).SQLDB()
	for _, statement := range []string{
		`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x')`,
		`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1')`,
		`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES('w1','active',1,3,'u1')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed control fixture: %v", err)
		}
	}

	state, advanced, err := synchronizeControlEpoch(
		ctx,
		db,
		config.DatabaseDriverSQLite,
		"w1",
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced || state.Epoch != 2 || state.BindingRevision != 3 || state.Mode != "active" {
		t.Fatalf("advanced state=%+v advanced=%t", state, advanced)
	}

	state, advanced, err = synchronizeControlEpoch(
		ctx,
		db,
		config.DatabaseDriverSQLite,
		"w1",
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if advanced || state.Epoch != 2 {
		t.Fatalf("idempotent state=%+v advanced=%t", state, advanced)
	}
}

func TestSynchronizeControlEpochRejectsUnexpectedGap(t *testing.T) {
	ctx := context.Background()
	controlPath := filepath.Join(t.TempDir(), "control-gap.db")
	controlConfig := storage.Config{
		Env:        "test",
		Driver:     storage.DriverSQLite,
		SQLitePath: controlPath,
	}
	provider := storagesqlite.Provider{}
	if err := provider.MigrateControl(ctx, controlConfig); err != nil {
		t.Fatal(err)
	}
	store, err := provider.OpenControl(ctx, controlConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db := store.(storage.SQLStore).SQLDB()
	for _, statement := range []string{
		`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x')`,
		`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1')`,
		`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES('w1','active',1,1,'u1')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed control fixture: %v", err)
		}
	}

	_, _, err = synchronizeControlEpoch(ctx, db, config.DatabaseDriverSQLite, "w1", 3)
	if err == nil || !strings.Contains(err.Error(), "cannot be synchronized") {
		t.Fatalf("unexpected-gap error = %v", err)
	}
}
