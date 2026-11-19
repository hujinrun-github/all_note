package japaneseintegration

import (
	"net/http"
	"testing"
	"time"
)

func TestSignatureCanonicalAndTamper(t *testing.T) {
	now := time.Unix(1795096800, 0)
	headers := http.Header{}
	Sign(headers, "POST", "/all-note/api/integrations/japanese/events?z=2&a=1", "conn", "v1", "secret", []byte(`{"completed":true}`), now, "0123456789abcdef0123456789abcdef")
	for _, tc := range []struct {
		name, target, body string
		want               bool
	}{
		{"canonical prefix", "/api/integrations/japanese/events?a=1&z=2", `{"completed":true}`, true},
		{"tampered body", "/api/integrations/japanese/events?a=1&z=2", `{"completed":false}`, false},
		{"tampered path", "/api/integrations/japanese/catalog?a=1&z=2", `{"completed":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifySignature(headers, "POST", tc.target, "secret", []byte(tc.body), now); got != tc.want {
				t.Fatalf("valid=%v want %v", got, tc.want)
			}
		})
	}
	if VerifySignature(headers, "POST", "/api/integrations/japanese/events?a=1&z=2", "secret", []byte(`{"completed":true}`), now.Add(6*time.Minute)) {
		t.Fatal("expired signature accepted")
	}
}
