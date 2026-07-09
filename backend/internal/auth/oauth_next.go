package auth

import (
	"net/url"
	"strings"
)

func SanitizeOAuthNext(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") {
		return "/"
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, `/\`) || strings.Contains(value, `\`) || strings.Contains(lower, "%5c") {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "/"
	}
	return value
}
