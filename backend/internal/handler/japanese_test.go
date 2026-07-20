package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
)

type recordingWorkspaceChat struct{ workspaceID string }

func (r *recordingWorkspaceChat) Generate(_ context.Context, workspaceID, _, _ string) (string, error) {
	r.workspaceID = workspaceID
	return `{"segments":[{"text":"近","reading":"ちか"},{"text":"く"}]}`, nil
}

func TestJapaneseFuriganaUsesRequestWorkspaceAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	chat := &recordingWorkspaceChat{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: "u1", WorkspaceID: "workspace-one"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.POST("/api/japanese/furigana", JapaneseFuriganaWithAI(chat))
	request := httptest.NewRequest(http.MethodPost, "/api/japanese/furigana", strings.NewReader(`{"text":"近く"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || chat.workspaceID != "workspace-one" || !strings.Contains(response.Body.String(), `"source":"ai"`) {
		t.Fatalf("status=%d workspace=%q body=%s", response.Code, chat.workspaceID, response.Body.String())
	}
}

func TestJapaneseFuriganaReturnsStructuredSegments(t *testing.T) {
	t.Setenv("AI_API_KEY", "")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/japanese/furigana", JapaneseFurigana)
	req := httptest.NewRequest(http.MethodPost, "/api/japanese/furigana", strings.NewReader(`{"text":"近く"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	var response struct {
		Data struct {
			Segments []model.FuriganaSegment `json:"segments"`
			Source   string                  `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := []model.FuriganaSegment{{Text: "近", Reading: "ちか"}, {Text: "く"}}
	if len(response.Data.Segments) != len(want) {
		t.Fatalf("segments = %#v, want %#v", response.Data.Segments, want)
	}
	for i := range want {
		if response.Data.Segments[i] != want[i] {
			t.Fatalf("segment %d = %#v, want %#v", i, response.Data.Segments[i], want[i])
		}
	}
	if response.Data.Source != "local" {
		t.Fatalf("source = %q, want local", response.Data.Source)
	}
}

func TestJapaneseFuriganaRejectsEmptyAndOversizedText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, text := range []string{"   ", strings.Repeat("日", maxFuriganaTextRunes+1)} {
		router := gin.New()
		router.POST("/api/japanese/furigana", JapaneseFurigana)
		body, _ := json.Marshal(map[string]string{"text": text})
		req := httptest.NewRequest(http.MethodPost, "/api/japanese/furigana", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
		}
	}
}
