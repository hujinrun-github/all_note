package handler

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/middleware"
)

func TestSettingsProfileIsUserScopedAndUpdatesAtomically(t *testing.T) {
	env := setupAuthTestEnv(t)
	registerSettingsProfileTestRoutes(env)
	token := "settings-profile-token"
	createAuthTestSession(t, env, authTestUserID, "session_settings_profile", token, time.Now().UTC().Add(time.Hour), false)

	request := httptest.NewRequest(http.MethodPatch, "/api/settings/profile", strings.NewReader(`{"display_name":"New Name","locale":"ja-JP","time_zone":"Asia/Tokyo"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(authTestCookie(env, token))
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"display_name":"New Name"`) || !strings.Contains(response.Body.String(), `"locale":"ja-JP"`) {
		t.Fatalf("update profile status=%d body=%s", response.Code, response.Body.String())
	}
	user, err := env.store.Auth().GetUserByID(t.Context(), authTestUserID)
	if err != nil || user.DisplayName != "New Name" {
		t.Fatalf("updated user=%+v err=%v", user, err)
	}
	profile, err := env.store.Auth().GetUserProfile(t.Context(), authTestUserID)
	if err != nil || profile.TimeZone != "Asia/Tokyo" {
		t.Fatalf("updated profile=%+v err=%v", profile, err)
	}
}

func TestSettingsAvatarValidatesStoresAndServesAuthenticatedImage(t *testing.T) {
	env := setupAuthTestEnv(t)
	registerSettingsProfileTestRoutes(env)
	token := "settings-avatar-token"
	createAuthTestSession(t, env, authTestUserID, "session_settings_avatar", token, time.Now().UTC().Add(time.Hour), false)
	contents := testPNG(t, 32, 24)

	request := httptest.NewRequest(http.MethodPut, "/api/settings/profile/avatar", bytes.NewReader(contents))
	request.Header.Set("Content-Type", "image/png")
	request.AddCookie(authTestCookie(env, token))
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"width":32`) || !strings.Contains(response.Body.String(), `"height":24`) {
		t.Fatalf("upload avatar status=%d body=%s", response.Code, response.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/settings/profile/avatar", nil)
	get.AddCookie(authTestCookie(env, token))
	imageResponse := httptest.NewRecorder()
	env.router.ServeHTTP(imageResponse, get)
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/png" || !bytes.Equal(imageResponse.Body.Bytes(), contents) {
		t.Fatalf("get avatar status=%d type=%s", imageResponse.Code, imageResponse.Header().Get("Content-Type"))
	}

	unauthenticated := httptest.NewRecorder()
	env.router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/settings/profile/avatar", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated avatar status=%d", unauthenticated.Code)
	}
}

func TestSettingsAvatarRejectsInvalidOrOversizedContent(t *testing.T) {
	env := setupAuthTestEnv(t)
	registerSettingsProfileTestRoutes(env)
	token := "settings-avatar-invalid-token"
	createAuthTestSession(t, env, authTestUserID, "session_settings_avatar_invalid", token, time.Now().UTC().Add(time.Hour), false)
	for name, testCase := range map[string]struct {
		contents []byte
		want     int
	}{
		"invalid":   {contents: []byte("not an image"), want: http.StatusBadRequest},
		"oversized": {contents: bytes.Repeat([]byte{1}, maxUserAvatarBytes+1), want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/settings/profile/avatar", bytes.NewReader(testCase.contents))
			request.AddCookie(authTestCookie(env, token))
			response := httptest.NewRecorder()
			env.router.ServeHTTP(response, request)
			if response.Code != testCase.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, testCase.want, response.Body.String())
			}
		})
	}
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value.Set(x, y, color.RGBA{R: 20, G: 80, B: 160, A: 255})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func registerSettingsProfileTestRoutes(env *authTestEnv) {
	authMiddleware := middleware.AuthMiddleware{Store: env.store, SessionSecret: env.cfg.SessionSecret, Cookie: env.cfg.Cookie}
	routes := env.router.Group("/api/settings")
	routes.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())
	routes.GET("/profile", GetSettingsProfile(env.store))
	routes.PATCH("/profile", UpdateSettingsProfile(env.store))
	routes.GET("/profile/avatar", GetSettingsAvatar(env.store))
	routes.PUT("/profile/avatar", PutSettingsAvatar(env.store))
	routes.DELETE("/profile/avatar", DeleteSettingsAvatar(env.store))
}
