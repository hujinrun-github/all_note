package codexoauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceAuthorizationAndTokenExchange(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"user_code": "ABCD-EFGH", "device_auth_id": "device-1", "interval": 3})
	})
	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_code": "code-1", "code_verifier": "verifier-1"})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("code_verifier") != "verifier-1" {
			t.Fatalf("verifier=%q", r.Form.Get("code_verifier"))
		}
		claims := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"account-1"}}`))
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "id_token": "header." + claims + ".signature"})
	})
	client := NewClient(server.Client(), server.URL, server.URL+"/oauth/token")
	device, err := client.Start(context.Background())
	if err != nil || device.UserCode != "ABCD-EFGH" {
		t.Fatalf("device=%+v err=%v", device, err)
	}
	grant, pending, err := client.Poll(context.Background(), device.DeviceAuthID, device.UserCode)
	if err != nil || pending {
		t.Fatalf("grant=%+v pending=%v err=%v", grant, pending, err)
	}
	tokens, err := client.Exchange(context.Background(), grant)
	if err != nil || tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" || tokens.AccountID != "account-1" {
		t.Fatalf("tokens=%+v err=%v", tokens, err)
	}
}

func TestPollTreatsAuthorizationPendingAsPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{}`, http.StatusForbidden) }))
	defer server.Close()
	_, pending, err := NewClient(server.Client(), server.URL, server.URL+"/oauth/token").Poll(context.Background(), "d", "u")
	if err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}
