package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type notionTargetConfig struct {
	DataSourceID             string `json:"data_source_id"`
	TokenEnv                 string `json:"token_env"`
	TitleProperty            string `json:"title_property"`
	FlowSpaceIDProperty      string `json:"flowspace_id_property"`
	FolderProperty           string `json:"folder_property"`
	TagsProperty             string `json:"tags_property"`
	FlowSpaceUpdatedProperty string `json:"flowspace_updated_property"`
}

func parseNotionTargetConfig(target *model.SyncTarget) (notionTargetConfig, error) {
	if target == nil {
		return notionTargetConfig{}, errors.New("sync target is required")
	}
	if target.Type != "notion" {
		return notionTargetConfig{}, fmt.Errorf("expected notion sync target, got %q", target.Type)
	}
	if !target.Enabled {
		return notionTargetConfig{}, errors.New("notion sync target is disabled")
	}

	var config notionTargetConfig
	raw := strings.TrimSpace(target.ConfigJSON)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return notionTargetConfig{}, fmt.Errorf("parse notion sync target config: %w", err)
	}

	config.DataSourceID = strings.TrimSpace(config.DataSourceID)
	config.TokenEnv = defaultString(config.TokenEnv, "FLOWSPACE_NOTION_TOKEN")
	config.TitleProperty = defaultString(config.TitleProperty, "Name")
	config.FlowSpaceIDProperty = defaultString(config.FlowSpaceIDProperty, "FlowSpace ID")
	config.FolderProperty = defaultString(config.FolderProperty, "Folder")
	config.TagsProperty = defaultString(config.TagsProperty, "Tags")
	config.FlowSpaceUpdatedProperty = defaultString(config.FlowSpaceUpdatedProperty, "FlowSpace Updated At")

	if config.DataSourceID == "" {
		return notionTargetConfig{}, errors.New("notion data source id is required")
	}
	return config, nil
}

func notionToken(config notionTargetConfig) (string, error) {
	envName := strings.TrimSpace(config.TokenEnv)
	if envName == "" {
		envName = "FLOWSPACE_NOTION_TOKEN"
	}
	if !isApprovedNotionTokenEnv(envName) {
		return "", errors.New("notion token env must be FLOWSPACE_NOTION_TOKEN or a FLOWSPACE_*NOTION_TOKEN variable")
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if token == "" {
		return "", fmt.Errorf("%s is required", envName)
	}
	return token, nil
}

func isApprovedNotionTokenEnv(name string) bool {
	return name == "FLOWSPACE_NOTION_TOKEN" || (strings.HasPrefix(name, "FLOWSPACE_") && strings.HasSuffix(name, "NOTION_TOKEN"))
}

func defaultString(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
