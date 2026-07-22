package tenantruntime

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestControlSourceLoadsVersionAndConsistentBindingSnapshot(t *testing.T) {
	db := openControlSourceTestDB(t)
	seedControlSourceRuntime(t, db)
	source, err := NewControlSource(db, ControlSQLite)
	if err != nil {
		t.Fatal(err)
	}

	version, err := source.LoadVersion(context.Background(), "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if version != (Version{WorkspaceID: "workspace-1", Mode: "active", Epoch: 7, BindingRevision: 11}) {
		t.Fatalf("version = %+v", version)
	}

	snapshot, err := source.LoadSnapshot(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != version || snapshot.DatabaseEndpointID != "database-endpoint" ||
		snapshot.ObjectEndpointID != "object-endpoint" || snapshot.ChatMode != "disabled" ||
		snapshot.ChatEndpointID != "" || snapshot.TranscriptionMode != "reuse_chat" ||
		snapshot.TranscriptionEndpointID != "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestControlSourceFailsClosedForMissingOrInvalidBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB)
	}{
		{
			name: "missing binding",
			mutate: func(t *testing.T, db *sql.DB) {
				mustControlSourceExec(t, db, `DELETE FROM workspace_service_bindings WHERE workspace_id='workspace-1' AND kind='object_s3'`)
			},
		},
		{
			name: "storage without endpoint",
			mutate: func(t *testing.T, db *sql.DB) {
				mustControlSourceExec(t, db, `UPDATE workspace_service_bindings SET endpoint_id=NULL WHERE workspace_id='workspace-1' AND kind='data_store'`)
			},
		},
		{
			name: "chat reuse mode",
			mutate: func(t *testing.T, db *sql.DB) {
				mustControlSourceExec(t, db, `UPDATE workspace_service_bindings SET mode='reuse_chat' WHERE workspace_id='workspace-1' AND kind='llm_chat'`)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openControlSourceTestDB(t)
			seedControlSourceRuntime(t, db)
			tt.mutate(t, db)
			source, err := NewControlSource(db, ControlSQLite)
			if err != nil {
				t.Fatal(err)
			}
			version, err := source.LoadVersion(context.Background(), "workspace-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.LoadSnapshot(context.Background(), version); !errors.Is(err, ErrRuntimeUnavailable) {
				t.Fatalf("LoadSnapshot error = %v", err)
			}
		})
	}
}

func TestControlSourceRejectsStaleExpectedVersion(t *testing.T) {
	db := openControlSourceTestDB(t)
	seedControlSourceRuntime(t, db)
	source, err := NewControlSource(db, ControlSQLite)
	if err != nil {
		t.Fatal(err)
	}
	version, err := source.LoadVersion(context.Background(), "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	mustControlSourceExec(t, db, `UPDATE workspace_runtime_state SET binding_revision=binding_revision+1 WHERE workspace_id='workspace-1'`)
	if _, err := source.LoadSnapshot(context.Background(), version); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("LoadSnapshot error = %v", err)
	}
}

func TestNewControlSourceRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewControlSource(nil, ControlSQLite); err == nil {
		t.Fatal("nil database was accepted")
	}
	db := openControlSourceTestDB(t)
	if _, err := NewControlSource(db, ControlDialect("mysql")); err == nil {
		t.Fatal("unsupported dialect was accepted")
	}
}

func openControlSourceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	mustControlSourceExec(t, db, `CREATE TABLE workspace_runtime_state (
		workspace_id TEXT PRIMARY KEY, mode TEXT NOT NULL, epoch INTEGER NOT NULL,
		binding_revision INTEGER NOT NULL
	)`)
	mustControlSourceExec(t, db, `CREATE TABLE workspace_service_bindings (
		workspace_id TEXT NOT NULL, kind TEXT NOT NULL, mode TEXT NOT NULL,
		endpoint_id TEXT, revision INTEGER NOT NULL,
		PRIMARY KEY(workspace_id,kind)
	)`)
	return db
}

func seedControlSourceRuntime(t *testing.T, db *sql.DB) {
	t.Helper()
	mustControlSourceExec(t, db, `INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision)
		VALUES('workspace-1','active',7,11)`)
	for _, statement := range []string{
		`INSERT INTO workspace_service_bindings VALUES('workspace-1','data_store','custom','database-endpoint',3)`,
		`INSERT INTO workspace_service_bindings VALUES('workspace-1','object_s3','default','object-endpoint',4)`,
		`INSERT INTO workspace_service_bindings VALUES('workspace-1','llm_chat','disabled',NULL,5)`,
		`INSERT INTO workspace_service_bindings VALUES('workspace-1','llm_transcription','reuse_chat',NULL,6)`,
	} {
		mustControlSourceExec(t, db, statement)
	}
}

func mustControlSourceExec(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}
