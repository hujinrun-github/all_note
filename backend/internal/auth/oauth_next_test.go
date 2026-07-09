package auth

import "testing"

func TestSanitizeOAuthNextAcceptsSafeRelativePaths(t *testing.T) {
	for _, input := range []string{"/", "/tasks", "/notes?id=1", "/editor/note_1#sync"} {
		if got := SanitizeOAuthNext(input); got != input {
			t.Fatalf("SanitizeOAuthNext(%q) = %q", input, got)
		}
	}
}

func TestSanitizeOAuthNextRejectsExternalAndBackslashPaths(t *testing.T) {
	for _, input := range []string{
		"",
		"https://evil.com/phishing",
		"//evil.com/phishing",
		`\evil.com`,
		`/\evil.com`,
		`/%5Cevil.com`,
		"tasks",
	} {
		if got := SanitizeOAuthNext(input); got != "/" {
			t.Fatalf("SanitizeOAuthNext(%q) = %q, want /", input, got)
		}
	}
}
