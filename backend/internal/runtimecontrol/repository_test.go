package runtimecontrol

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

func TestRuntimeStateCASAndAtomicBindingActivation(t *testing.T) {
	db := createRuntimeControlFixture(t)
	repository, err := New(db, DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repository.BeginOperation(context.Background(), "w1", 1, "migration", "m1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "draining" || state.Epoch != 2 || state.OperationID != "m1" {
		t.Fatalf("unexpected draining state: %+v", state)
	}
	if _, err := repository.BeginOperation(context.Background(), "w1", 1, "migration", "m1", "u1"); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("duplicate begin error=%v", err)
	}
	state, err = repository.Transition(context.Background(), "w1", "m1", 2, "draining", "migrating", "u1")
	if err != nil || state.Mode != "migrating" {
		t.Fatalf("transition to migrating: state=%+v err=%v", state, err)
	}
	state, err = repository.Transition(context.Background(), "w1", "m1", 2, "migrating", "activating", "u1")
	if err != nil || state.Mode != "activating" {
		t.Fatalf("transition to activating: state=%+v err=%v", state, err)
	}
	state, err = repository.ActivateBinding(context.Background(), Activation{
		WorkspaceID: "w1", OperationID: "m1", ExpectedEpoch: 2, ExpectedBindingRowRevision: 1, ExpectedRuntimeBindingRevision: 1,
		BindingKind: "data_store", BindingMode: "default", EndpointSourceType: "system", EndpointID: "db2", ActorUserID: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != "active" || state.BindingRevision != 2 || state.OperationID != "" {
		t.Fatalf("unexpected activated state: %+v", state)
	}
	var endpointID string
	var revision int64
	if err := db.QueryRow(`SELECT endpoint_id,revision FROM workspace_service_bindings WHERE workspace_id='w1' AND kind='data_store'`).Scan(&endpointID, &revision); err != nil {
		t.Fatal(err)
	}
	if endpointID != "db2" || revision != 2 {
		t.Fatalf("binding endpoint=%s revision=%d", endpointID, revision)
	}
}

func TestActivationCASFailureRollsBackBinding(t *testing.T) {
	db := createRuntimeControlFixture(t)
	repository, _ := New(db, DialectSQLite)
	_, err := repository.ActivateBinding(context.Background(), Activation{
		WorkspaceID: "w1", OperationID: "m1", ExpectedEpoch: 99, ExpectedBindingRowRevision: 1, ExpectedRuntimeBindingRevision: 1,
		BindingKind: "data_store", BindingMode: "default", EndpointSourceType: "system", EndpointID: "db2", ActorUserID: "u1",
	})
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("activation error=%v", err)
	}
	var endpointID string
	var revision int64
	if err := db.QueryRow(`SELECT endpoint_id,revision FROM workspace_service_bindings WHERE workspace_id='w1' AND kind='data_store'`).Scan(&endpointID, &revision); err != nil {
		t.Fatal(err)
	}
	if endpointID != "db1" || revision != 1 {
		t.Fatalf("binding changed despite rollback: endpoint=%s revision=%d", endpointID, revision)
	}
}

func createRuntimeControlFixture(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	statements := []string{
		`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x')`,
		`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1')`,
		`INSERT INTO system_profile_families(id,kind,name) VALUES('sf','data_store','system db')`,
		`INSERT INTO system_profile_versions(id,family_id,kind,version,provider,state) VALUES('sv','sf','data_store',1,'postgres','verified')`,
		`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,system_profile_version_id) VALUES('db1','w1','data_store','system','sv'),('db2','w1','data_store','system','sv')`,
		`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES('w1','data_store','default','system','db1','u1')`,
		`INSERT INTO storage_transition_jobs(id,workspace_id,operation_kind,source_endpoint_type,source_endpoint_id,source_provider,target_endpoint_type,target_endpoint_id,target_provider,source_installation_id,source_database_identity,source_schema_identity,target_installation_id,target_database_identity,target_schema_identity,source_binding_revision,source_runtime_revision,migration_epoch,state,created_by) VALUES('m1','w1','migration','system','db1','postgres','system','db2','postgres','i1','d1','s1','i2','d2','s2',1,1,2,'pending','u1')`,
		`INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES('w1','active',1,1,'u1')`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed runtime fixture: %v\n%s", err, statement)
		}
	}
	return db
}
