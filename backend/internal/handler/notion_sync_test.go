package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
)

func TestSyncTargetFromRequestPreservesNotionTypeAndConfig(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
		Enabled:    true,
		AutoSync:   false,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "notion" {
		t.Fatalf("type = %q, want notion", target.Type)
	}
	if target.ConfigJSON != `{"data_source_id":"ds-123","title_property":"Name"}` {
		t.Fatalf("config_json = %q", target.ConfigJSON)
	}
	if target.VaultPath != "" || target.BaseFolder != "" {
		t.Fatalf("notion target should not require local path fields: %+v", target)
	}
}

func TestSyncTargetFromRequestDefaultsTypeToObsidian(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Name:       "Local Vault",
		VaultPath:  "D:\\Vault",
		BaseFolder: "FlowSpace Notes",
		Enabled:    true,
	}

	target := syncTargetFromRequest(req)

	if target.Type != "obsidian" {
		t.Fatalf("type = %q, want obsidian", target.Type)
	}
	if target.ConfigJSON != "{}" {
		t.Fatalf("config_json = %q, want {}", target.ConfigJSON)
	}
}

func TestValidateSyncTargetRequestRejectsObsidianWithoutPaths(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Name: "Local Vault",
	}

	target := syncTargetFromRequest(req)

	err := validateSyncTargetRequest(target)
	if err == nil {
		t.Fatal("expected validation error for obsidian target without paths")
	}
	if err.Error() != "obsidian sync target requires vault_path and base_folder" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestValidateSyncTargetRequestAllowsNotionWithoutPaths(t *testing.T) {
	req := &model.SaveSyncTargetRequest{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123","title_property":"Name"}`,
	}

	target := syncTargetFromRequest(req)

	if err := validateSyncTargetRequest(target); err != nil {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateSyncTargetRequestRejectsSaveObsidianWithoutPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/sync/targets", bytes.NewBufferString(`{"name":"Local Vault"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	SaveSyncTarget(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "obsidian sync target requires vault_path and base_folder") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestValidateSyncTargetRequestRejectsUpdateObsidianWithoutPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "target-1"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/sync/targets/target-1", bytes.NewBufferString(`{"name":"Local Vault"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	UpdateSyncTarget(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "obsidian sync target requires vault_path and base_folder") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}
