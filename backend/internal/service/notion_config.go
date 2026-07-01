package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

var notionIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

type notionTargetConfig struct {
	DataSourceID             string   `json:"data_source_id"`
	Token                    string   `json:"token"`
	TokenEnv                 string   `json:"token_env"`
	TitleProperty            string   `json:"title_property"`
	FlowSpaceIDProperty      string   `json:"flowspace_id_property"`
	FolderProperty           string   `json:"folder_property"`
	TagsProperty             string   `json:"tags_property"`
	FlowSpaceUpdatedProperty string   `json:"flowspace_updated_property"`
	RequiredTags             []string `json:"required_tags"`
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

	config.DataSourceID = notionResourceIDFromInput(config.DataSourceID)
	config.Token = strings.TrimSpace(config.Token)
	config.TokenEnv = defaultString(config.TokenEnv, "FLOWSPACE_NOTION_TOKEN")
	config.TitleProperty = defaultString(config.TitleProperty, "Name")
	config.FlowSpaceIDProperty = strings.TrimSpace(config.FlowSpaceIDProperty)
	config.FolderProperty = defaultString(config.FolderProperty, "Folder")
	config.TagsProperty = defaultString(config.TagsProperty, "Tags")
	config.FlowSpaceUpdatedProperty = defaultString(config.FlowSpaceUpdatedProperty, "FlowSpace Updated At")
	config.RequiredTags = normalizeSyncTags(config.RequiredTags)

	if config.DataSourceID == "" {
		return notionTargetConfig{}, errors.New("notion data source id is required")
	}
	return config, nil
}

func notionResourceIDFromInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	searchValue := value
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		searchValue = parsed.Path
	}
	matches := notionIDPattern.FindAllString(searchValue, -1)
	if len(matches) == 0 {
		return value
	}
	return normalizeNotionID(matches[len(matches)-1])
}

func normalizeNotionID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 36 {
		return value
	}
	if len(value) != 32 {
		return value
	}
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
}

func notionToken(config notionTargetConfig) (string, error) {
	if token := strings.TrimSpace(config.Token); token != "" {
		return token, nil
	}
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
