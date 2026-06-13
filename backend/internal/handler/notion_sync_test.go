package handler

import (
	"testing"

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
