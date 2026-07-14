package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilesync"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestPostgresMobileMutationHTTPReplayGate(t *testing.T) {
	schema := fmt.Sprintf("fs_test_mobile_http_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	opened, err := (Provider{}).Open(ctx, storage.Config{
		Env:    "test",
		Driver: storage.DriverPostgres,
		URL:    createPostgresTestSchema(t, schema),
	})
	if err != nil {
		t.Fatalf("open postgres store: %v", err)
	}
	defer opened.Close()
	store := opened.(*store)
	workspaceID := "mobile_http_workspace"
	requestContext := seedPostgresMobileHTTPWorkspace(t, store, workspaceID)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(requestContext)
		c.Next()
	})
	router.POST("/api/mobile/sync/mutations", handler.ApplyMobileMutations(store))

	const (
		deviceClientID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		mutationID     = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		entityClientID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	)
	request := func(title string) mobilesync.MutationResult {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"client_id": deviceClientID,
			"mutations": []map[string]any{{
				"mutation_id": mutationID,
				"operation":   model.MobileOperationNoteCreate,
				"entity_id":   entityClientID,
				"payload":     map[string]any{"title": title},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/mobile/sync/mutations", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var batch mobilesync.BatchResult
		if err := json.Unmarshal(response.Body.Bytes(), &batch); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(batch.Results) != 1 {
			t.Fatalf("results=%d body=%s", len(batch.Results), response.Body.String())
		}
		return batch.Results[0]
	}

	created := request("Lost response note")
	if created.Status != model.MobileMutationApplied || created.Entity == nil || created.Entity.Revision != 1 {
		t.Fatalf("unexpected initial result: %+v", created)
	}
	for sample := 1; sample <= 100; sample++ {
		replayed := request("Lost response note")
		if replayed.Status != model.MobileMutationApplied || replayed.Entity == nil || replayed.Entity.EntityID != created.Entity.EntityID || replayed.Entity.Revision != 1 {
			t.Fatalf("replay sample %d = %+v, want original result", sample, replayed)
		}
	}
	for sample := 1; sample <= 100; sample++ {
		rejected := request(fmt.Sprintf("Changed payload %d", sample))
		if rejected.Status != "rejected" || rejected.Error == nil || rejected.Error.Code != "mutation_id_reused" {
			t.Fatalf("changed sample %d = %+v, want mutation_id_reused", sample, rejected)
		}
	}

	assertPostgresMobileHTTPCount(t, store, `SELECT COUNT(*) FROM notes WHERE workspace_id = $1 AND client_id = $2`, workspaceID, entityClientID)
	assertPostgresMobileHTTPCount(t, store, `SELECT COUNT(*) FROM mobile_mutation_receipts WHERE workspace_id = $1 AND mutation_id = $2`, workspaceID, mutationID)
	assertPostgresMobileHTTPCount(t, store, `SELECT COUNT(*) FROM mobile_sync_outbox WHERE workspace_id = $1 AND mutation_id = $2`, workspaceID, mutationID)
}

func seedPostgresMobileHTTPWorkspace(t *testing.T, store storage.Store, workspaceID string) context.Context {
	t.Helper()
	now := time.Now().Unix()
	userID := workspaceID + "_owner"
	ctx := auth.ContextWithWorkspaceScope(context.Background(), workspaceID)
	if err := store.Transact(ctx, func(tx storage.Store) error {
		if err := tx.Auth().CreateUser(context.Background(), &model.User{
			ID:                 userID,
			Email:              workspaceID + "@example.com",
			DisplayName:        workspaceID,
			PasswordHash:       "test-only-hash",
			DefaultWorkspaceID: workspaceID,
			Role:               "admin",
			Status:             "active",
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
		if err := tx.Auth().CreateWorkspace(context.Background(), &model.Workspace{
			ID: workspaceID, Name: workspaceID, OwnerUserID: userID, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.Auth().AddWorkspaceMember(context.Background(), workspaceID, userID, "owner"); err != nil {
			return err
		}
		return provisioning.EnsureDefaultWorkspaceData(ctx, tx)
	}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	return ctx
}

func assertPostgresMobileHTTPCount(t *testing.T, store *store, query string, args ...any) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("query mobile gate count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d, want 1 for %s", count, query)
	}
}
