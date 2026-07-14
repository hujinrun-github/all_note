package testsupport

import (
	"strings"
	"testing"
)

func TestIntegrationTargetFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantValue   string
		wantReady   bool
		wantErrPart string
	}{
		{
			name:      "configured target is ready",
			env:       map[string]string{"TARGET_URL": "postgres://test"},
			wantValue: "postgres://test",
			wantReady: true,
		},
		{
			name:      "missing optional target is unavailable",
			env:       map[string]string{},
			wantReady: false,
		},
		{
			name:        "missing required target fails",
			env:         map[string]string{"REQUIRE_TARGET": "true"},
			wantErrPart: "TARGET_URL",
		},
		{
			name:        "invalid required flag fails",
			env:         map[string]string{"REQUIRE_TARGET": "sometimes"},
			wantErrPart: "REQUIRE_TARGET",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			gotValue, gotReady, err := IntegrationTargetFromEnv("test target", "TARGET_URL", "REQUIRE_TARGET", getenv)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrPart, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotValue != tt.wantValue || gotReady != tt.wantReady {
				t.Fatalf("got value=%q ready=%v, want value=%q ready=%v", gotValue, gotReady, tt.wantValue, tt.wantReady)
			}
		})
	}
}
