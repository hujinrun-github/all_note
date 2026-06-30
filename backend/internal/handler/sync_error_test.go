package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func TestObsidianDeletionErrorMapsBindingConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	obsidianDeletionError(c, service.ErrSyncBindingConflict)

	assertSyncBindingConflictResponse(t, recorder)
}

func TestNotionDeletionErrorMapsBindingConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	notionDeletionError(c, service.ErrSyncBindingConflict)

	assertSyncBindingConflictResponse(t, recorder)
}

func TestTargetDeletionErrorMapsBindingRequired(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	targetDeletionError(c, service.ErrSyncBindingRequired)

	assertSyncBindingErrorResponse(t, recorder, "binding_required")
}

func TestTargetDeletionErrorMapsBindingConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	targetDeletionError(c, service.ErrSyncBindingConflict)

	assertSyncBindingErrorResponse(t, recorder, "binding_mismatch")
}

func assertSyncBindingConflictResponse(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	assertSyncBindingErrorResponse(t, recorder, "binding_mismatch")
}

func assertSyncBindingErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	var body model.APIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error == nil || body.Error.Code != code {
		t.Fatalf("error = %+v; body = %s", body.Error, recorder.Body.String())
	}
}
