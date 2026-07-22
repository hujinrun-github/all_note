package codexsubscription

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/codexoauth"
	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

type allowAuthorizer struct{}

func (allowAuthorizer) CanManageWorkspace(context.Context, string, string) (bool, error) {
	return true, nil
}

type fakeOAuth struct {
	pending      bool
	refreshCalls int
}

func (f *fakeOAuth) Start(context.Context) (codexoauth.DeviceAuthorization, error) {
	return codexoauth.DeviceAuthorization{DeviceAuthID: "device-secret", UserCode: "ABCD-EFGH", VerificationURL: "https://auth.openai.com/codex/device", Interval: 5 * time.Second, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *fakeOAuth) Poll(context.Context, string, string) (codexoauth.AuthorizationGrant, bool, error) {
	return codexoauth.AuthorizationGrant{Code: "grant", Verifier: "verifier"}, f.pending, nil
}
func (f *fakeOAuth) Exchange(context.Context, codexoauth.AuthorizationGrant) (codexoauth.Tokens, error) {
	return codexoauth.Tokens{AccessToken: "access-secret", RefreshToken: "refresh-secret", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (f *fakeOAuth) Refresh(context.Context, string) (codexoauth.Tokens, error) {
	f.refreshCalls++
	return codexoauth.Tokens{AccessToken: "refreshed-access", RefreshToken: "rotated-refresh", AccountID: "account", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestDeviceAuthorizationActivatesEncryptedCodexProfile(t *testing.T) {
	service, profiles, db := newFixture(t, &fakeOAuth{})
	started, err := service.Start(t.Context(), "u1", "w1")
	if err != nil || started.UserCode != "ABCD-EFGH" {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	result, err := service.Poll(t.Context(), "u1", "w1", started.FlowID, 0, 1)
	if err != nil || result.Status != "connected" {
		t.Fatalf("poll=%+v err=%v", result, err)
	}
	binding, err := profiles.GetBinding(t.Context(), "w1", "llm_chat")
	if err != nil || binding.Mode != "custom" || binding.EndpointID != result.EndpointID {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	secret, err := profiles.DecryptSecret(t.Context(), "w1", "llm_chat", result.ProfileVersionID)
	if err != nil || !bytes.Contains(secret, []byte("refresh-secret")) {
		t.Fatalf("secret=%q err=%v", secret, err)
	}
	clear(secret)
	var raw []byte
	if err := db.QueryRow(`SELECT secret_ciphertext FROM workspace_profile_versions WHERE id=?`, result.ProfileVersionID).Scan(&raw); err != nil || bytes.Contains(raw, []byte("access-secret")) {
		t.Fatal("Codex access token was not encrypted")
	}
	// Completing the same browser poll is idempotent and does not create another version.
	again, err := service.Poll(t.Context(), "u1", "w1", started.FlowID, binding.Revision, 2)
	if err != nil || again != result {
		t.Fatalf("retry=%+v err=%v", again, err)
	}
}

func TestPendingDeviceAuthorizationDoesNotCreateProfile(t *testing.T) {
	service, _, db := newFixture(t, &fakeOAuth{pending: true})
	started, _ := service.Start(t.Context(), "u1", "w1")
	result, err := service.Poll(t.Context(), "u1", "w1", started.FlowID, 0, 1)
	if err != nil || result.Status != "pending" {
		t.Fatalf("poll=%+v err=%v", result, err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM workspace_profile_versions`).Scan(&count)
	if count != 0 {
		t.Fatalf("pending flow created %d profiles", count)
	}
}

func TestRefreshCodexCredentialsCreatesImmutableVersionAndAdvancesBinding(t *testing.T) {
	oauth := &fakeOAuth{}
	service, profiles, db := newFixture(t, oauth)
	started, _ := service.Start(t.Context(), "u1", "w1")
	connected, err := service.Poll(t.Context(), "u1", "w1", started.FlowID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := profiles.DecryptSecret(t.Context(), "w1", "llm_chat", connected.ProfileVersionID)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.RefreshCodexCredentials(t.Context(), "w1", airuntime.ResolvedCapability{
		Mode: "custom", EndpointID: connected.EndpointID, ProfileVersionID: connected.ProfileVersionID,
		Provider: "openai_codex_subscription", ConfigJSON: `{"endpoint":"https://example.test","model":"gpt-test"}`, Secret: secret,
	})
	clear(secret)
	if err != nil || refreshed.ProfileVersionID == connected.ProfileVersionID || oauth.refreshCalls != 1 {
		t.Fatalf("refreshed=%+v calls=%d err=%v", refreshed, oauth.refreshCalls, err)
	}
	binding, err := profiles.GetBinding(t.Context(), "w1", "llm_chat")
	if err != nil || binding.EndpointID != refreshed.EndpointID || binding.Revision != 2 {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	var runtimeRevision int64
	if err := db.QueryRow(`SELECT binding_revision FROM workspace_runtime_state WHERE workspace_id='w1'`).Scan(&runtimeRevision); err != nil || runtimeRevision != 3 {
		t.Fatalf("runtime revision=%d err=%v", runtimeRevision, err)
	}
	clear(refreshed.Secret)
}

func newFixture(t *testing.T, oauth OAuthClient) (*Service, *controlprofile.Repository, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x'); INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1'); INSERT INTO workspace_members(workspace_id,user_id,role) VALUES('w1','u1','owner'); INSERT INTO workspace_runtime_state(workspace_id,mode,epoch,binding_revision,updated_by) VALUES('w1','active',1,1,'u1')`); err != nil {
		t.Fatal(err)
	}
	keyring, _ := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{9}, 32)})
	flows, _ := codexoauth.NewRepository(db, codexoauth.DialectSQLite, keyring)
	profiles, _ := controlprofile.New(db, controlprofile.DialectSQLite, keyring)
	runtimeRepository, _ := runtimecontrol.New(db, runtimecontrol.DialectSQLite)
	service, err := New(oauth, flows, profiles, runtimeRepository, allowAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	return service, profiles, db
}
