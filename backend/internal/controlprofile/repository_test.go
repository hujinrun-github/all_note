package controlprofile

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

func TestCreateVersionPreservesSecretUsingNewAADAndNonce(t *testing.T) {
	repository, db := createProfileFixture(t)
	if err := repository.CreateFamily(context.Background(), "w1", "family", "data_store", "Database", "u1"); err != nil {
		t.Fatal(err)
	}
	first, err := repository.CreateVersion(context.Background(), CreateVersionInput{
		ID: "v1", FamilyID: "family", WorkspaceID: "w1", Kind: "data_store", Provider: "postgres", ConfigJSON: `{"host":"db1"}`, Secret: []byte("password-one"), CreatedBy: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkVerified(context.Background(), "w1", "data_store", "v1"); err != nil {
		t.Fatal(err)
	}
	second, err := repository.CreateVersion(context.Background(), CreateVersionInput{
		ID: "v2", FamilyID: "family", WorkspaceID: "w1", Kind: "data_store", Provider: "postgres", ConfigJSON: `{"host":"db2"}`, PreserveFromVersionID: "v1", CreatedBy: "u1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || second.Version != 2 || !second.HasSecret || second.KeyID != "active" {
		t.Fatalf("unexpected versions: first=%+v second=%+v", first, second)
	}
	secret, err := repository.DecryptSecret(context.Background(), "w1", "data_store", "v2")
	if err != nil || string(secret) != "password-one" {
		t.Fatalf("preserved secret=%q err=%v", secret, err)
	}
	var firstCiphertext, secondCiphertext, firstNonce, secondNonce []byte
	if err := db.QueryRow(`SELECT secret_ciphertext,secret_nonce FROM workspace_profile_versions WHERE workspace_id='w1' AND kind='data_store' AND id='v1'`).Scan(&firstCiphertext, &firstNonce); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT secret_ciphertext,secret_nonce FROM workspace_profile_versions WHERE workspace_id='w1' AND kind='data_store' AND id='v2'`).Scan(&secondCiphertext, &secondNonce); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstCiphertext, secondCiphertext) || bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("preserve copied ciphertext/nonce instead of re-encrypting under new AAD")
	}
}

func TestEndpointRequiresVerifiedMatchingWorkspaceProfile(t *testing.T) {
	repository, db := createProfileFixture(t)
	if err := repository.CreateFamily(context.Background(), "w1", "family", "object_s3", "Objects", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateVersion(context.Background(), CreateVersionInput{ID: "v1", FamilyID: "family", WorkspaceID: "w1", Kind: "object_s3", Provider: "minio", ConfigJSON: `{}`, Secret: []byte("secret"), CreatedBy: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWorkspaceEndpoint(context.Background(), "w1", "object-1", "object_s3", "v1"); err == nil {
		t.Fatal("draft profile accepted by endpoint")
	}
	if err := repository.MarkVerified(context.Background(), "w1", "object_s3", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWorkspaceEndpoint(context.Background(), "w1", "object-1", "object_s3", "v1"); err != nil {
		t.Fatalf("verified endpoint: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u2','u2@example.test','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w2','two','u2')`); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWorkspaceEndpoint(context.Background(), "w2", "stolen", "object_s3", "v1"); err == nil {
		t.Fatal("cross-workspace profile accepted")
	}
	if err := repository.RetireWorkspaceVersion(context.Background(), "w1", "object_s3", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateWorkspaceEndpoint(context.Background(), "w1", "object-2", "object_s3", "v1"); err == nil {
		t.Fatal("retired profile accepted by new endpoint")
	}
}

func TestBindingModesAndRevisionCAS(t *testing.T) {
	repository, _ := createProfileFixture(t)
	system, _, err := repository.ReconcileSystemCandidate(context.Background(), ReconcileSystemInput{CandidateID: "chat-v1", FamilyID: "chat-family", Kind: "llm_chat", Name: "Chat", Provider: "openai", ConfigJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkSystemVerified(context.Background(), "llm_chat", system.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSystemEndpoint(context.Background(), "w1", "chat-default", "llm_chat", system.ID); err != nil {
		t.Fatal(err)
	}
	binding, err := repository.SetBinding(context.Background(), SetBindingInput{WorkspaceID: "w1", Kind: "llm_chat", Mode: "default", EndpointSourceType: "system", EndpointID: "chat-default", ExpectedRevision: 0, ActorUserID: "u1"})
	if err != nil || binding.Revision != 1 {
		t.Fatalf("initial binding=%+v err=%v", binding, err)
	}
	binding, err = repository.SetBinding(context.Background(), SetBindingInput{WorkspaceID: "w1", Kind: "llm_chat", Mode: "disabled", ExpectedRevision: 1, ActorUserID: "u1"})
	if err != nil || binding.Revision != 2 || binding.EndpointID != "" {
		t.Fatalf("disabled binding=%+v err=%v", binding, err)
	}
	_, err = repository.SetBinding(context.Background(), SetBindingInput{WorkspaceID: "w1", Kind: "llm_chat", Mode: "default", EndpointSourceType: "system", EndpointID: "chat-default", ExpectedRevision: 1, ActorUserID: "u1"})
	if !errors.Is(err, ErrBindingCASConflict) {
		t.Fatalf("stale binding revision error=%v", err)
	}
	if _, err := repository.SetBinding(context.Background(), SetBindingInput{WorkspaceID: "w1", Kind: "llm_transcription", Mode: "reuse_chat", EndpointSourceType: "system", EndpointID: "chat-default", ActorUserID: "u1"}); err == nil {
		t.Fatal("reuse_chat accepted an endpoint")
	}
}

func TestVerifiedProfileVersionCannotBeEditedInPlace(t *testing.T) {
	repository, db := createProfileFixture(t)
	if err := repository.CreateFamily(context.Background(), "w1", "family", "object_s3", "Objects", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateVersion(context.Background(), CreateVersionInput{ID: "v1", FamilyID: "family", WorkspaceID: "w1", Kind: "object_s3", Provider: "minio", ConfigJSON: `{}`, Secret: []byte("secret"), CreatedBy: "u1"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkVerified(context.Background(), "w1", "object_s3", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_profile_versions SET config_json='{"changed":true}' WHERE workspace_id='w1' AND kind='object_s3' AND id='v1'`); err == nil {
		t.Fatal("verified profile was mutable in place")
	}
}

func TestSystemEnvironmentChangeCreatesCandidateWithoutChangingBinding(t *testing.T) {
	repository, db := createProfileFixture(t)
	first, created, err := repository.ReconcileSystemCandidate(context.Background(), ReconcileSystemInput{
		CandidateID: "system-v1", FamilyID: "system-db", Kind: "data_store", Name: "Platform database", Provider: "postgres", ConfigJSON: `{ "host": "db1" }`, Secret: []byte("password-1"),
	})
	if err != nil || !created || first.Version != 1 {
		t.Fatalf("first reconcile: version=%+v created=%v err=%v", first, created, err)
	}
	if err := repository.MarkSystemVerified(context.Background(), "data_store", "system-v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_service_endpoints(id,workspace_id,kind,source_type,system_profile_version_id) VALUES('default-db','w1','data_store','system','system-v1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_service_bindings(workspace_id,kind,mode,endpoint_source_type,endpoint_id,updated_by) VALUES('w1','data_store','default','system','default-db','u1')`); err != nil {
		t.Fatal(err)
	}
	same, created, err := repository.ReconcileSystemCandidate(context.Background(), ReconcileSystemInput{
		CandidateID: "unused-id", FamilyID: "system-db", Kind: "data_store", Name: "Platform database", Provider: "postgres", ConfigJSON: `{"host":"db1"}`, Secret: []byte("password-1"),
	})
	if err != nil || created || same.ID != "system-v1" {
		t.Fatalf("unchanged environment created candidate: version=%+v created=%v err=%v", same, created, err)
	}
	second, created, err := repository.ReconcileSystemCandidate(context.Background(), ReconcileSystemInput{
		CandidateID: "system-v2", FamilyID: "system-db", Kind: "data_store", Name: "Platform database", Provider: "postgres", ConfigJSON: `{"host":"db2"}`, Secret: []byte("password-2"),
	})
	if err != nil || !created || second.Version != 2 || second.State != "draft" {
		t.Fatalf("changed environment candidate: version=%+v created=%v err=%v", second, created, err)
	}
	var boundVersion string
	if err := db.QueryRow(`SELECT system_profile_version_id FROM workspace_service_endpoints WHERE workspace_id='w1' AND kind='data_store' AND id='default-db'`).Scan(&boundVersion); err != nil {
		t.Fatal(err)
	}
	if boundVersion != "system-v1" {
		t.Fatalf("candidate silently changed active endpoint to %s", boundVersion)
	}
}

func createProfileFixture(t *testing.T) (*Repository, *sql.DB) {
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
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1')`); err != nil {
		t.Fatal(err)
	}
	keyring, err := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := New(db, DialectSQLite, keyring)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db
}
