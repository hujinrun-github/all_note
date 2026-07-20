package airuntime

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

func TestControlSourceResolvesConcreteEncryptedProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	cfg := storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path}
	if err := (storagesqlite.Provider{}).MigrateControl(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO users(id,email,password_hash) VALUES('u1','u1@example.test','x'); INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','one','u1')`); err != nil {
		t.Fatal(err)
	}
	keyring, _ := credentials.NewKeyring("active", map[string][]byte{"active": bytes.Repeat([]byte{4}, 32)})
	profiles, _ := controlprofile.New(db, controlprofile.DialectSQLite, keyring)
	version, _, err := profiles.ReconcileSystemCandidate(context.Background(), controlprofile.ReconcileSystemInput{CandidateID: "chat-v1", FamilyID: "chat", Kind: "llm_chat", Name: "Chat", Provider: "openai", ConfigJSON: `{"model":"gpt-test"}`, Secret: []byte("api-key")})
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.MarkSystemVerified(context.Background(), "llm_chat", version.ID); err != nil {
		t.Fatal(err)
	}
	if err := profiles.CreateSystemEndpoint(context.Background(), "w1", "chat-default", "llm_chat", version.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := profiles.SetBinding(context.Background(), controlprofile.SetBindingInput{WorkspaceID: "w1", Kind: "llm_chat", Mode: "default", EndpointSourceType: "system", EndpointID: "chat-default", ActorUserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_ai_feature_settings(workspace_id,feature,enabled,fallback_mode,updated_by) VALUES('w1','roadmap_generation',1,'template','u1')`); err != nil {
		t.Fatal(err)
	}
	source, _ := NewControlSource(db, ControlSQLite, keyring)
	resolver, _ := NewResolver(source)
	resolved, err := resolver.ResolveChat(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ProfileVersionID != "chat-v1" || resolved.Model != "gpt-test" || string(resolved.Secret) != "api-key" {
		t.Fatalf("resolved profile=%+v", resolved)
	}
	if _, err := db.Exec(`UPDATE system_profile_versions SET state='retired' WHERE kind='llm_chat' AND id='chat-v1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveChat(context.Background(), "w1"); err != nil {
		t.Fatalf("historically bound retired profile must remain readable: %v", err)
	}
}
