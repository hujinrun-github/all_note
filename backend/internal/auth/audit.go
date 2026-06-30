package auth

import "strings"

func SanitizeAuditMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	sanitized := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if isSecretAuditKey(key) {
			continue
		}
		sanitized[key] = sanitizeAuditValue(value)
	}
	return sanitized
}

func sanitizeAuditValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return SanitizeAuditMetadata(typed)
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretAuditKey(key) {
				continue
			}
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeAuditValue(item))
		}
		return out
	default:
		return value
	}
}

func isSecretAuditKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, marker := range []string{
		"password",
		"token",
		"cookie",
		"authorization",
		"secret",
		"api_key",
		"access_key",
		"credential",
		"bearer",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
