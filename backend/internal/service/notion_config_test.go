package service

import (
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
)

func TestParseNotionTargetConfigUsesDefaults(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}

	config, err := parseNotionTargetConfig(target)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if config.DataSourceID != "ds-123" {
		t.Fatalf("data source id = %q", config.DataSourceID)
	}
	if config.TokenEnv != "FLOWSPACE_NOTION_TOKEN" {
		t.Fatalf("token env = %q", config.TokenEnv)
	}
	if config.TitleProperty != "Name" {
		t.Fatalf("title property = %q", config.TitleProperty)
	}
	if config.FlowSpaceIDProperty != "FlowSpace ID" {
		t.Fatalf("flowspace id property = %q", config.FlowSpaceIDProperty)
	}
	if config.TagsProperty != "Tags" {
		t.Fatalf("tags property = %q", config.TagsProperty)
	}
}

func TestParseNotionTargetConfigRejectsMissingDataSource(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "notion",
		Name:       "Personal Notion",
		ConfigJSON: `{}`,
		Enabled:    true,
	}

	_, err := parseNotionTargetConfig(target)
	if err == nil || !strings.Contains(err.Error(), "notion data source id is required") {
		t.Fatalf("expected missing data source error, got %v", err)
	}
}

func TestParseNotionTargetConfigRejectsNonNotionTarget(t *testing.T) {
	target := &model.SyncTarget{
		Type:       "obsidian",
		Name:       "Local Vault",
		ConfigJSON: `{"data_source_id":"ds-123"}`,
		Enabled:    true,
	}

	_, err := parseNotionTargetConfig(target)
	if err == nil || !strings.Contains(err.Error(), "expected notion sync target") {
		t.Fatalf("expected wrong target error, got %v", err)
	}
}

func TestLoadNotionTokenFromConfiguredEnv(t *testing.T) {
	t.Setenv("FLOWSPACE_TEST_NOTION_TOKEN", "secret-token")

	config := notionTargetConfig{TokenEnv: "FLOWSPACE_TEST_NOTION_TOKEN"}
	token, err := notionToken(config)
	if err != nil {
		t.Fatalf("load token: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestLoadNotionTokenRejectsEmptyEnv(t *testing.T) {
	config := notionTargetConfig{TokenEnv: "FLOWSPACE_EMPTY_NOTION_TOKEN"}
	_, err := notionToken(config)
	if err == nil || !strings.Contains(err.Error(), "FLOWSPACE_EMPTY_NOTION_TOKEN is required") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}
