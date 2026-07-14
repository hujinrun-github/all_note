package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
)

func TestWatchBearerIsRestrictedAndRevocationTakesEffect(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
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
	routes := registeredRoutes(Setup(env.config))
	for _, route := range []string{
		"POST /api/devices/watch/authorize",
		"POST /api/devices/watch/revoke",
		"POST /api/voice-notes",
		"PUT /api/voice-notes/:clientID/audio",
		"GET /api/voice-notes/:clientID/audio",
		"GET /api/voice-notes/:clientID/status",
		"POST /api/voice-notes/:clientID/transcription",
		"GET /api/watch/snapshot",
		"PATCH /api/watch/tasks/:id",
	} {
		if !routes[route] {
			t.Fatalf("route %s is not registered", route)
		}
	}
}
