package postgres

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func unixToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func timeToUnix(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}

func tagsJSONStringToArray(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}

	var input []string
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	tags := make([]string, 0, len(input))
	for _, item := range input {
		tag := strings.TrimSpace(strings.TrimLeft(item, "#"))
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return tags, nil
}

func tagsArrayToJSONString(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	data, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func normalizeJSONObjectString(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}

	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", err
	}
	if value == nil {
		return "", fmt.Errorf("JSON value must be an object")
	}

	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(raw)); err != nil {
		return "", err
	}
	return compacted.String(), nil
}
