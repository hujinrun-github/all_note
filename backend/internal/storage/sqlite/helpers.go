package sqlite

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func nowUnix() int64 {
	return time.Now().Unix()
}

func normalizeTagsJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "[]", nil
	}

	var input []string
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", err
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

	data, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
