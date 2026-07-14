package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/mobilesync"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestMobileMutationRouteIsAuthenticatedIdempotentAndCASProtected(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	ctx := authpkg.ContextWithWorkspaceScope(t.Context(), routerTestWorkspaceID)
	if err := provisioning.EnsureDefaultWorkspaceData(ctx, env.store); err != nil {
		t.Fatalf("seed workspace defaults: %v", err)
	}
	sessionToken := "mobile-mutation-session-token"
	createRouterSession(t, env, sessionToken)
	router := Setup(env.config)

	clientID := "11111111-1111-4111-8111-111111111111"
	mutationID := "22222222-2222-4222-8222-222222222222"
	entityID := "33333333-3333-4333-8333-333333333333"
	request := func(mutation, title string, baseRevision *int64) *httptest.ResponseRecorder {
		payload := map[string]any{
			"client_id": clientID,
			"mutations": []map[string]any{{
				"mutation_id":   mutation,
				"operation":     map[bool]string{true: "note.update", false: "note.create"}[baseRevision != nil],
				"entity_id":     entityID,
				"base_revision": baseRevision,
				"payload":       map[string]any{"title": title},
			}},
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/mobile/sync/mutations", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	decode := func(response *httptest.ResponseRecorder) mobilesync.BatchResult {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("mutation status=%d body=%s", response.Code, response.Body.String())
		}
		var result mobilesync.BatchResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode mutation result: %v", err)
		}
		return result
	}

	createdResponse := request(mutationID, "Offline route note", nil)
	if !bytes.Contains(createdResponse.Body.Bytes(), []byte(`"entity_id":"`)) ||
		bytes.Contains(createdResponse.Body.Bytes(), []byte(`"client_id":`)) ||
		bytes.Contains(createdResponse.Body.Bytes(), []byte(`"id":`)) {
		t.Fatalf("mutation response violates public entity envelope: %s", createdResponse.Body.String())
	}
	created := decode(createdResponse)
	if len(created.Results) != 1 || created.Results[0].Status != model.MobileMutationApplied || created.Results[0].Entity == nil || created.Results[0].Entity.Revision != 1 {
		t.Fatalf("unexpected create response: %+v", created)
	}
	replayed := decode(request(mutationID, "Offline route note", nil))
	if len(replayed.Results) != 1 || replayed.Results[0].Entity == nil || replayed.Results[0].Entity.EntityID != created.Results[0].Entity.EntityID {
		t.Fatalf("replay created another entity: first=%+v replay=%+v", created, replayed)
	}
	changed := decode(request(mutationID, "Changed payload", nil))
	if len(changed.Results) != 1 || changed.Results[0].Error == nil || changed.Results[0].Error.Code != "mutation_id_reused" {
		t.Fatalf("changed replay = %+v, want mutation_id_reused", changed)
	}
	staleBase := int64(0)
	stale := decode(request("44444444-4444-4444-8444-444444444444", "Stale update", &staleBase))
	if len(stale.Results) != 1 || stale.Results[0].Status != model.MobileMutationConflict || stale.Results[0].Error == nil || stale.Results[0].Error.Code != "revision_conflict" {
		t.Fatalf("stale update = %+v, want revision conflict", stale)
	}

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/mobile/sync/mutations", bytes.NewBufferString(`{"client_id":"x","mutations":[]}`))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse := httptest.NewRecorder()
	router.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticatedResponse.Code, unauthenticatedResponse.Body.String())
	}
}

func TestMobileMutationRouteRegistrationFollowsFeatureFlag(t *testing.T) {
	disabled := setupRouterAuthEnv(t, false)
	if registeredRoutes(Setup(disabled.config))["POST /api/mobile/sync/mutations"] {
		t.Fatal("mobile mutation route must be absent while mobile_sync_v1 is disabled")
	}
	if !registeredRoutes(Setup(disabled.config))["POST /api/voice-notes/:clientID/transcription"] {
		t.Fatal("legacy transcription route should remain available while mobile_sync_v1 is disabled")
	}

	enabled := setupRouterAuthEnv(t, false)
	enabled.config.MobileSyncV1Enabled = true
	if !registeredRoutes(Setup(enabled.config))["POST /api/mobile/sync/mutations"] {
		t.Fatal("mobile mutation route must be registered when mobile_sync_v1 is enabled")
	}
	if !registeredRoutes(Setup(enabled.config))["GET /api/mobile/sync/changes"] ||
		!registeredRoutes(Setup(enabled.config))["GET /api/mobile/sync/snapshot"] {
		t.Fatal("mobile read routes must be registered when mobile_sync_v1 is enabled")
	}
	if !registeredRoutes(Setup(enabled.config))["POST /api/mobile/transcription-jobs/:jobID/retry"] {
		t.Fatal("mobile transcription retry route must be registered when mobile_sync_v1 is enabled")
	}
	if registeredRoutes(Setup(enabled.config))["POST /api/voice-notes/:clientID/transcription"] {
		t.Fatal("legacy synchronous transcription route must be absent while mobile_sync_v1 is enabled")
	}
}

func TestMobileReadRoutesReturnOpaqueCursorImmutableSnapshotAndResyncError(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	ctx := authpkg.ContextWithWorkspaceScope(t.Context(), routerTestWorkspaceID)
	if err := provisioning.EnsureDefaultWorkspaceData(ctx, env.store); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.Notes().Create(ctx, &model.CreateNoteRequest{
		Title: "Read route note", Body: "Offline body", FolderID: "__uncategorized", Tags: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	sessionToken := "mobile-read-route-session"
	createRouterSession(t, env, sessionToken)
	router := Setup(env.config)

	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	changesResponse := request("/api/mobile/sync/changes?scope=iphone")
	if changesResponse.Code != http.StatusOK {
		t.Fatalf("changes status=%d body=%s", changesResponse.Code, changesResponse.Body.String())
	}
	var changes struct {
		SchemaVersion string `json:"schema_version"`
		Changes       []struct {
			EntityID string `json:"entity_id"`
			Revision int64  `json:"revision"`
		} `json:"changes"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.Unmarshal(changesResponse.Body.Bytes(), &changes); err != nil {
		t.Fatal(err)
	}
	if changes.SchemaVersion != "mobile-v1" || len(changes.Changes) != 1 || changes.Changes[0].EntityID == "" || changes.Changes[0].Revision != 1 || changes.NextCursor == "" {
		t.Fatalf("changes response=%+v body=%s", changes, changesResponse.Body.String())
	}
	tampered := request("/api/mobile/sync/changes?scope=iphone&cursor=" + changes.NextCursor + "x")
	if tampered.Code != http.StatusConflict || !bytes.Contains(tampered.Body.Bytes(), []byte(`"resync_required":true`)) {
		t.Fatalf("tampered cursor status=%d body=%s", tampered.Code, tampered.Body.String())
	}
	snapshotResponse := request("/api/mobile/sync/snapshot?scope=iphone")
	if snapshotResponse.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", snapshotResponse.Code, snapshotResponse.Body.String())
	}
	var snapshot struct {
		SchemaVersion  string `json:"schema_version"`
		Entities       []any  `json:"entities"`
		SnapshotCursor string `json:"snapshot_cursor"`
		ValidUntil     string `json:"scope_valid_until"`
		HasMore        bool   `json:"has_more"`
	}
	if err := json.Unmarshal(snapshotResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != "mobile-v1" || len(snapshot.Entities) == 0 || snapshot.SnapshotCursor == "" || snapshot.ValidUntil == "" || snapshot.HasMore {
		t.Fatalf("snapshot response=%+v body=%s", snapshot, snapshotResponse.Body.String())
	}
}

func TestMobileTranscriptionJobRoutesPersistAndReturnWithoutCallingProvider(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	ctx := authpkg.ContextWithWorkspaceScope(t.Context(), routerTestWorkspaceID)
	if err := provisioning.EnsureDefaultWorkspaceData(ctx, env.store); err != nil {
		t.Fatalf("seed workspace defaults: %v", err)
	}
	note, err := env.store.Notes().Create(ctx, &model.CreateNoteRequest{Title: "Async transcription", FolderID: "__uncategorized", Tags: "[]"})
	if err != nil {
		t.Fatalf("create backing note: %v", err)
	}
	nativeStore, err := storage.NativeStoreFrom(env.store)
	if err != nil {
		t.Fatalf("native store: %v", err)
	}
	voiceClientID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	now := time.Now().UTC().Unix()
	if err := nativeStore.VoiceNotes().Create(ctx, &model.VoiceNote{
		ID: uuid.NewString(), ClientID: voiceClientID, NoteID: note.ID, DurationMS: 1000, RecordedAt: now,
		Language: "zh", ObjectKey: "test/async.m4a", MimeType: "audio/mp4", AudioSize: 5,
		AudioSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		UploadState: model.VoiceUploadUploaded, TranscriptionState: model.TranscriptionNotStarted,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create uploaded voice: %v", err)
	}
	sessionToken := "mobile-transcription-job-session"
	createRouterSession(t, env, sessionToken)
	router := Setup(env.config)

	post := func(mutationID, language string) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]string{"language": language})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/mobile/voice-notes/"+voiceClientID+"/transcriptions", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", mutationID)
		request.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	decodeJob := func(response *httptest.ResponseRecorder, wantStatus int) model.TranscriptionJob {
		t.Helper()
		if response.Code != wantStatus {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		var job model.TranscriptionJob
		if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		return job
	}

	mutationID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	created := decodeJob(post(mutationID, "zh"), http.StatusAccepted)
	if created.State != model.TranscriptionJobQueued || created.Generation != 1 || created.Revision != 1 {
		t.Fatalf("created job = %+v", created)
	}
	replayed := decodeJob(post(mutationID, "zh"), http.StatusAccepted)
	if replayed.JobID != created.JobID {
		t.Fatalf("replay job ID = %q, want %q", replayed.JobID, created.JobID)
	}
	changed := post(mutationID, "en")
	if changed.Code != http.StatusConflict || !bytes.Contains(changed.Body.Bytes(), []byte(`"code":"mutation_id_reused"`)) {
		t.Fatalf("changed request status=%d body=%s", changed.Code, changed.Body.String())
	}
	converged := decodeJob(post("ffffffff-ffff-4fff-8fff-ffffffffffff", "zh"), http.StatusAccepted)
	if converged.JobID != created.JobID {
		t.Fatalf("second path job ID = %q, want %q", converged.JobID, created.JobID)
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/transcription-jobs/"+created.JobID, nil)
	getRequest.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, getRequest)
	got := decodeJob(getResponse, http.StatusOK)
	if got.JobID != created.JobID || got.State != model.TranscriptionJobQueued {
		t.Fatalf("GET job = %+v", got)
	}

	worker, err := storage.TranscriptionJobWorkerRepositoryFrom(env.store)
	if err != nil {
		t.Fatal(err)
	}
	at := now + 10
	for attempt := int64(1); attempt <= 6; attempt++ {
		lease, err := worker.ClaimNext(t.Context(), model.ClaimTranscriptionJob{
			WorkerID: "router-worker", LeaseToken: "router-lease-" + string(rune('0'+attempt)),
			Now: at, LeaseExpiresAt: at + 120,
		})
		if err != nil {
			t.Fatalf("claim attempt %d: %v", attempt, err)
		}
		if _, err := worker.Fail(t.Context(), model.FailTranscriptionJob{
			JobID: created.JobID, LeaseToken: lease.LeaseToken, ErrorCode: "provider_failed",
			NextAttemptAt: at + 1, Now: at,
		}); err != nil {
			t.Fatalf("fail attempt %d: %v", attempt, err)
		}
		at++
	}
	retryMutationID := "12121212-1212-4212-8212-121212121212"
	retryRequest := httptest.NewRequest(http.MethodPost, "/api/mobile/transcription-jobs/"+created.JobID+"/retry", nil)
	retryRequest.Header.Set("Idempotency-Key", retryMutationID)
	retryRequest.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
	retryResponse := httptest.NewRecorder()
	router.ServeHTTP(retryResponse, retryRequest)
	retried := decodeJob(retryResponse, http.StatusAccepted)
	if retried.JobID == created.JobID || retried.Generation != 2 || retried.State != model.TranscriptionJobQueued {
		t.Fatalf("retried job = %+v", retried)
	}
}

func TestWatchBearerIsRestrictedAndRevocationTakesEffect(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	env.config.VoiceObjects = objectstore.NewMemoryStore()
	env.config.MaxVoiceBytes = 1024 * 1024
	sessionToken := "native-routes-session-token"
	createRouterSession(t, env, sessionToken)
	router := Setup(env.config)

	authorizeBody := bytes.NewBufferString(`{"name":"Route Test Watch","expires_in_days":30}`)
	authorizeRequest := httptest.NewRequest(http.MethodPost, "/api/devices/watch/authorize", authorizeBody)
	authorizeRequest.Header.Set("Content-Type", "application/json")
	authorizeRequest.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
	authorizeResponse := httptest.NewRecorder()
	router.ServeHTTP(authorizeResponse, authorizeRequest)
	if authorizeResponse.Code != http.StatusCreated {
		t.Fatalf("authorize status = %d, want 201; body = %s", authorizeResponse.Code, authorizeResponse.Body.String())
	}
	var authorization struct {
		Data struct {
			Device model.WatchDevice `json:"device"`
			Token  string            `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(authorizeResponse.Body.Bytes(), &authorization); err != nil {
		t.Fatalf("decode authorization response: %v", err)
	}
	if authorization.Data.Token == "" || authorization.Data.Device.ID == "" {
		t.Fatalf("authorization response missing token or device: %s", authorizeResponse.Body.String())
	}

	snapshotRequest := httptest.NewRequest(http.MethodGet, "/api/watch/snapshot", nil)
	snapshotRequest.Header.Set("Authorization", "Bearer "+authorization.Data.Token)
	snapshotResponse := httptest.NewRecorder()
	router.ServeHTTP(snapshotResponse, snapshotRequest)
	if snapshotResponse.Code != http.StatusOK {
		t.Fatalf("watch snapshot status = %d, want 200; body = %s", snapshotResponse.Code, snapshotResponse.Body.String())
	}

	phoneScopeRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/sync/changes?scope=iphone", nil)
	phoneScopeRequest.Header.Set("Authorization", "Bearer "+authorization.Data.Token)
	phoneScopeResponse := httptest.NewRecorder()
	router.ServeHTTP(phoneScopeResponse, phoneScopeRequest)
	if phoneScopeResponse.Code != http.StatusForbidden {
		t.Fatalf("watch credential phone scope status = %d, want 403; body = %s", phoneScopeResponse.Code, phoneScopeResponse.Body.String())
	}
	watchScopeRequest := httptest.NewRequest(http.MethodGet, "/api/mobile/sync/changes?scope=watch", nil)
	watchScopeRequest.Header.Set("Authorization", "Bearer "+authorization.Data.Token)
	watchScopeResponse := httptest.NewRecorder()
	router.ServeHTTP(watchScopeResponse, watchScopeRequest)
	if watchScopeResponse.Code != http.StatusOK {
		t.Fatalf("watch credential watch scope status = %d, want 200; body = %s", watchScopeResponse.Code, watchScopeResponse.Body.String())
	}

	notesRequest := httptest.NewRequest(http.MethodGet, "/api/notes", nil)
	notesRequest.Header.Set("Authorization", "Bearer "+authorization.Data.Token)
	notesResponse := httptest.NewRecorder()
	router.ServeHTTP(notesResponse, notesRequest)
	if notesResponse.Code != http.StatusUnauthorized {
		t.Fatalf("watch token on general notes status = %d, want 401; body = %s", notesResponse.Code, notesResponse.Body.String())
	}

	revokeBody, err := json.Marshal(model.RevokeWatchDeviceRequest{DeviceID: authorization.Data.Device.ID})
	if err != nil {
		t.Fatalf("encode revoke request: %v", err)
	}
	revokeRequest := httptest.NewRequest(http.MethodPost, "/api/devices/watch/revoke", bytes.NewReader(revokeBody))
	revokeRequest.Header.Set("Content-Type", "application/json")
	revokeRequest.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: sessionToken})
	revokeResponse := httptest.NewRecorder()
	router.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204; body = %s", revokeResponse.Code, revokeResponse.Body.String())
	}

	revokedRequest := httptest.NewRequest(http.MethodGet, "/api/watch/snapshot", nil)
	revokedRequest.Header.Set("Authorization", "Bearer "+authorization.Data.Token)
	revokedResponse := httptest.NewRecorder()
	router.ServeHTTP(revokedResponse, revokedRequest)
	if revokedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked watch token status = %d, want 401; body = %s", revokedResponse.Code, revokedResponse.Body.String())
	}
}

func TestNativeRoutesAreRegistered(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	env.config.MobileSyncV1Enabled = true
	routes := registeredRoutes(Setup(env.config))
	for _, route := range []string{
		"POST /api/devices/watch/authorize",
		"POST /api/devices/watch/revoke",
		"POST /api/voice-notes",
		"PUT /api/voice-notes/:clientID/audio",
		"GET /api/voice-notes/:clientID/audio",
		"GET /api/voice-notes/:clientID/status",
		"POST /api/mobile/sync/mutations",
		"POST /api/mobile/voice-notes/:clientID/transcriptions",
		"GET /api/mobile/transcription-jobs/:jobID",
		"GET /api/watch/snapshot",
		"PATCH /api/watch/tasks/:id",
	} {
		if !routes[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}
