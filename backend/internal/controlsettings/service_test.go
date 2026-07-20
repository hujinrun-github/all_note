package controlsettings

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

type fixedAuthorizer bool

func (a fixedAuthorizer) CanManageWorkspace(context.Context, string, string) (bool, error) {
	return bool(a), nil
}

type captureProber struct{ secret, config []byte }

func (p *captureProber) Probe(_ context.Context, _, _ string, config, secret []byte) (ProbeResult, error) {
	p.config, p.secret = append([]byte(nil), config...), append([]byte(nil), secret...)
	return ProbeResult{Code: "OK", Message: "verified", InstallationID: "install", SchemaIdentity: "public"}, nil
}

func TestServiceSavesEncryptedDraftAndSeparatesProbe(t *testing.T) {
	profiles, runtimeRepository := createControlSettingsFixture(t)
	prober := &captureProber{}
	service, err := New(profiles, runtimeRepository, fixedAuthorizer(true), prober)
	if err != nil {
		t.Fatal(err)
	}
	objectSecret := `{"access_key":"access","secret_key":"secret-value"}`
	saved, err := service.SaveProfile(context.Background(), "u1", "w1", handler.SaveServiceProfileRequest{ID: "v1", FamilyID: "f1", Kind: "object_s3", Name: "Objects", Provider: "minio", Config: map[string]any{"endpoint": "https://objects.example", "bucket": "notes-test"}, Secret: objectSecret})
	if err != nil {
		t.Fatal(err)
	}
	if saved.State != "draft" || !saved.HasCredentials {
		t.Fatalf("saved=%+v", saved)
	}
	secret, err := profiles.DecryptSecret(context.Background(), "w1", "object_s3", "v1")
	if err != nil || string(secret) != objectSecret {
		t.Fatalf("decrypted=%q err=%v", secret, err)
	}
	if len(prober.secret) != 0 {
		t.Fatal("saving unexpectedly probed the endpoint")
	}
	result, err := service.TestProfile(context.Background(), "u1", "w1", handler.TestServiceProfileRequest{Kind: "object_s3", Provider: "minio", Config: map[string]any{"endpoint": "https://objects.example", "bucket": "notes-test"}, Secret: objectSecret})
	if err != nil || !result.OK || string(prober.secret) != objectSecret {
		t.Fatalf("probe result=%+v secret=%q err=%v", result, prober.secret, err)
	}
}

func TestServiceValidatesDatabaseSchemaAndObjectBucket(t *testing.T) {
	profiles, runtimeRepository := createControlSettingsFixture(t)
	service, _ := New(profiles, runtimeRepository, fixedAuthorizer(true), &captureProber{})
	_, err := service.SaveProfile(context.Background(), "u1", "w1", handler.SaveServiceProfileRequest{ID: "db-v1", FamilyID: "db-family", Kind: "data_store", Name: "DB", Provider: "postgres", Config: map[string]any{"endpoint": "postgres://example/db", "schema": "bad-name"}})
	if err == nil {
		t.Fatal("invalid schema was accepted")
	}
	_, err = service.SaveProfile(context.Background(), "u1", "w1", handler.SaveServiceProfileRequest{ID: "obj-v1", FamilyID: "obj-family", Kind: "object_s3", Name: "Objects", Provider: "minio", Config: map[string]any{"endpoint": "https://objects.example", "bucket": "Bad_Bucket"}})
	if err == nil {
		t.Fatal("invalid bucket was accepted")
	}
}

func TestServiceRequiresStructuredObjectCredentials(t *testing.T) {
	profiles, runtimeRepository := createControlSettingsFixture(t)
	service, _ := New(profiles, runtimeRepository, fixedAuthorizer(true), &captureProber{})
	base := handler.SaveServiceProfileRequest{ID: "obj-v1", FamilyID: "obj-family", Kind: "object_s3", Name: "Objects", Provider: "minio", Config: map[string]any{"endpoint": "https://objects.example", "bucket": "notes-test"}}
	for _, secret := range []string{"", "one-value", `{"access_key":"only"}`} {
		base.Secret = secret
		if _, err := service.SaveProfile(context.Background(), "u1", "w1", base); err == nil {
			t.Fatalf("invalid object credentials %q were accepted", secret)
		}
	}
}

func TestServiceRejectsNonOwnerBeforeReadingOrWritingProfiles(t *testing.T) {
	profiles, runtimeRepository := createControlSettingsFixture(t)
	service, _ := New(profiles, runtimeRepository, fixedAuthorizer(false), &captureProber{})
	_, err := service.SaveProfile(context.Background(), "u2", "w1", handler.SaveServiceProfileRequest{ID: "v1", FamilyID: "f1", Kind: "data_store", Name: "DB", Provider: "postgres", Config: map[string]any{}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner error=%v", err)
	}
}

func TestServiceVerifiesStoredProfileAndCreatesEndpoint(t *testing.T) {
	profiles, runtimeRepository := createControlSettingsFixture(t)
	prober := &captureProber{}
	service, _ := New(profiles, runtimeRepository, fixedAuthorizer(true), prober)
	_, err := service.SaveProfile(context.Background(), "u1", "w1", handler.SaveServiceProfileRequest{ID: "chat-v1", FamilyID: "chat-family", Kind: "llm_chat", Name: "Chat", Provider: "openai_compatible", Config: map[string]any{"endpoint": "https://ai.example/v1"}, Secret: "stored-key"})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := service.VerifyProfile(context.Background(), "u1", "w1", "llm_chat", "chat-v1")
	if err != nil {
		t.Fatal(err)
	}
	if verified.EndpointID != "custom-chat-v1" || string(prober.secret) != "stored-key" {
		t.Fatalf("verified=%+v secret=%q", verified, prober.secret)
	}
	version, err := profiles.GetVersion(context.Background(), "w1", "llm_chat", "chat-v1")
	if err != nil || version.State != "verified" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
}

func createControlSettingsFixture(t *testing.T) (*controlprofile.Repository, *runtimecontrol.Repository) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x'); INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1'); INSERT INTO workspace_members(workspace_id,user_id,role) VALUES('w1','u1','owner')`); err != nil {
		t.Fatal(err)
	}
	keyring, _ := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{9}, 32)})
	profiles, err := controlprofile.New(db, controlprofile.DialectSQLite, keyring)
	if err != nil {
		t.Fatal(err)
	}
	runtimeRepository, err := runtimecontrol.New(db, runtimecontrol.DialectSQLite)
	if err != nil {
		t.Fatal(err)
	}
	return profiles, runtimeRepository
}
