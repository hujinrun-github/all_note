package mobilev2contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

func RequestDigest(command []byte) (string, error) {
	value, err := parseJCSJSON(command)
	if err != nil {
		return "", err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("command envelope must be an object")
	}
	delete(object, "request_digest")
	delete(object, "forwarded_by_device_client_id")
	normalizeMobileV2SemanticSets(object)
	return digestJCSValue(object)
}

func PageChecksum(snapshotID string, pageIndex int, asOfSequence string, entitiesJSON []byte) (string, error) {
	value, err := parseJCSJSON(entitiesJSON)
	if err != nil {
		return "", err
	}
	entities, ok := value.([]any)
	if !ok {
		return "", fmt.Errorf("entities must be an array")
	}
	sortEntityValues(entities)
	return digestJCSValue(map[string]any{
		"snapshot_id":    snapshotID,
		"page_index":     json.Number(strconv.Itoa(pageIndex)),
		"as_of_sequence": asOfSequence,
		"entities":       entities,
	})
}

func ManifestChecksum(snapshotID, asOfSequence, scopeGeneration string, pageChecksums []string) (string, error) {
	checksums := make([]any, len(pageChecksums))
	for index, checksum := range pageChecksums {
		checksums[index] = checksum
	}
	return digestJCSValue(map[string]any{
		"snapshot_id":      snapshotID,
		"as_of_sequence":   asOfSequence,
		"scope_generation": scopeGeneration,
		"page_checksums":   checksums,
	})
}

func digestJCSValue(value any) (string, error) {
	var canonical bytes.Buffer
	if err := writeJCSValue(&canonical, value); err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical.Bytes())
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func normalizeMobileV2SemanticSets(value any) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			normalizeMobileV2SemanticSets(child)
			array, ok := child.([]any)
			if !ok {
				continue
			}
			switch key {
			case "entities":
				sortEntityValues(array)
			case "identity_mappings":
				sortIdentityMappings(array)
			case "field_paths":
				sort.SliceStable(array, func(left, right int) bool {
					return lessCodePoint(fmt.Sprint(array[left]), fmt.Sprint(array[right]))
				})
			}
		}
	case []any:
		for _, child := range value {
			normalizeMobileV2SemanticSets(child)
		}
	}
}

func sortEntityValues(values []any) {
	sort.SliceStable(values, func(left, right int) bool {
		leftObject, _ := values[left].(map[string]any)
		rightObject, _ := values[right].(map[string]any)
		leftType := stringValue(leftObject["entity_type"])
		rightType := stringValue(rightObject["entity_type"])
		if leftType != rightType {
			return lessCodePoint(leftType, rightType)
		}
		return lessCodePoint(canonicalEntityIdentity(leftObject), canonicalEntityIdentity(rightObject))
	})
}

func canonicalEntityIdentity(entity map[string]any) string {
	if entityType := stringValue(entity["entity_type"]); entityType == "schedule_version" {
		if payload, ok := entity["payload"].(map[string]any); ok {
			taskID := stringValue(payload["task_id"])
			revision := stringValue(payload["schedule_revision"])
			if taskID != "" && revision != "" {
				return "k:" + taskID + "#" + revision
			}
		}
	}
	if entityID := stringValue(entity["entity_id"]); entityID != "" {
		return "s:" + entityID
	}
	return "c:" + stringValue(entity["client_id"])
}

func sortIdentityMappings(values []any) {
	sort.SliceStable(values, func(left, right int) bool {
		leftObject, _ := values[left].(map[string]any)
		rightObject, _ := values[right].(map[string]any)
		leftKey := []string{stringValue(leftObject["entity_type"]), stringValue(leftObject["client_id"]), stringValue(leftObject["entity_id"])}
		rightKey := []string{stringValue(rightObject["entity_type"]), stringValue(rightObject["client_id"]), stringValue(rightObject["entity_id"])}
		for index := range leftKey {
			if leftKey[index] != rightKey[index] {
				return lessCodePoint(leftKey[index], rightKey[index])
			}
		}
		return false
	})
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func lessCodePoint(left, right string) bool {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	limit := len(leftRunes)
	if len(rightRunes) < limit {
		limit = len(rightRunes)
	}
	for index := 0; index < limit; index++ {
		if leftRunes[index] != rightRunes[index] {
			return leftRunes[index] < rightRunes[index]
		}
	}
	return len(leftRunes) < len(rightRunes)
}
