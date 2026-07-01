package auth

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeAuditMetadataRemovesNestedSecrets(t *testing.T) {
	metadata := map[string]any{
		"ip":     "127.0.0.1",
		"secret": "top-secret",
		"nested": map[string]any{
			"api_key": "api-secret",
			"safe":    "kept",
		},
		"items": []any{
			map[string]any{
				"access_key": "access-secret",
				"name":       "kept-item",
			},
			map[string]string{
				"secret_key": "secret-key-value",
				"label":      "kept-label",
			},
		},
	}

	sanitized := SanitizeAuditMetadata(metadata)

	raw := marshalAuditMetadataForTest(t, sanitized)
	for _, forbidden := range []string{"top-secret", "api-secret", "access-secret", "secret-key-value", "api_key", "access_key", "secret_key"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("sanitized metadata contains forbidden value %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(raw, "kept") || !strings.Contains(raw, "kept-item") || !strings.Contains(raw, "kept-label") {
		t.Fatalf("sanitized metadata removed safe values: %s", raw)
	}
}

func TestSanitizeAuditMetadataRemovesProviderSecretKeys(t *testing.T) {
	metadata := map[string]any{
		"minio_secret_key": "minio-secret",
		"secret_key":       "secret-key",
		"api_key":          "api-key",
		"access_key":       "access-key",
		"credential":       "credential-value",
		"bearer":           "bearer-value",
		"public":           "safe-value",
	}

	sanitized := SanitizeAuditMetadata(metadata)

	raw := marshalAuditMetadataForTest(t, sanitized)
	for _, forbidden := range []string{"minio-secret", "secret-key", "api-key", "access-key", "credential-value", "bearer-value"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("sanitized metadata contains forbidden value %q: %s", forbidden, raw)
		}
	}
	if sanitized["public"] != "safe-value" {
		t.Fatalf("safe metadata value missing: %#v", sanitized)
	}
}

func TestSanitizeAuditMetadataRemovesSecretsFromTypedAnyMapSlices(t *testing.T) {
	metadata := map[string]any{
		"items": []map[string]any{
			{
				"api_key": "leak",
				"safe":    "ok",
			},
		},
	}

	sanitized := SanitizeAuditMetadata(metadata)

	raw := marshalAuditMetadataForTest(t, sanitized)
	if strings.Contains(raw, "leak") || strings.Contains(raw, "api_key") {
		t.Fatalf("sanitized metadata contains typed slice secret: %s", raw)
	}
	if !strings.Contains(raw, "ok") {
		t.Fatalf("sanitized metadata removed safe typed slice value: %s", raw)
	}
}

func TestSanitizeAuditMetadataRemovesSecretsFromTypedStringMapSlices(t *testing.T) {
	metadata := map[string]any{
		"items": []map[string]string{
			{
				"access_key": "leak",
				"name":       "ok",
			},
		},
	}

	sanitized := SanitizeAuditMetadata(metadata)

	raw := marshalAuditMetadataForTest(t, sanitized)
	if strings.Contains(raw, "leak") || strings.Contains(raw, "access_key") {
		t.Fatalf("sanitized metadata contains typed string map slice secret: %s", raw)
	}
	if !strings.Contains(raw, "ok") {
		t.Fatalf("sanitized metadata removed safe typed string map slice value: %s", raw)
	}
}

func marshalAuditMetadataForTest(t *testing.T, metadata map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return strings.ToLower(string(raw))
}
