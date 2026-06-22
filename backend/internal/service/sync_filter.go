package service

import (
	"encoding/json"
	"strings"

	"github.com/hujinrun/flowspace/internal/model"
)

type syncFilterConfig struct {
	RequiredTags []string `json:"required_tags"`
}

func requiredSyncTagsFromTarget(target *model.SyncTarget) []string {
	if target == nil {
		return nil
	}
	raw := strings.TrimSpace(target.ConfigJSON)
	if raw == "" {
		raw = "{}"
	}
	var config syncFilterConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil
	}
	return normalizeSyncTags(config.RequiredTags)
}

func noteMatchesRequiredSyncTags(note model.Note, requiredTags []string) bool {
	return tagsMatchRequiredSyncTags(parseTags(note.Tags), requiredTags)
}

func tagsMatchRequiredSyncTags(tags []string, requiredTags []string) bool {
	required := normalizeSyncTags(requiredTags)
	if len(required) == 0 {
		return false
	}

	requiredSet := make(map[string]struct{}, len(required))
	for _, tag := range required {
		requiredSet[tag] = struct{}{}
	}
	for _, tag := range tags {
		if _, ok := requiredSet[canonicalSyncTag(tag)]; ok {
			return true
		}
	}
	return false
}

func syncTagsJSON(tags []string) string {
	cleaned := cleanSyncTags(tags)
	if len(cleaned) == 0 {
		return "[]"
	}
	data, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func normalizeSyncTags(tags []string) []string {
	cleaned := cleanSyncTags(tags)
	normalized := make([]string, 0, len(cleaned))
	seen := make(map[string]struct{}, len(cleaned))
	for _, tag := range cleaned {
		key := canonicalSyncTag(tag)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	return normalized
}

func cleanSyncTags(tags []string) []string {
	cleaned := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := cleanSyncTag(tag)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}

func cleanSyncTag(tag string) string {
	tag = strings.TrimSpace(tag)
	tag = strings.TrimLeft(tag, "#")
	return strings.TrimSpace(tag)
}

func canonicalSyncTag(tag string) string {
	return strings.ToLower(cleanSyncTag(tag))
}
