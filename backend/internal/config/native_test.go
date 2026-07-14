package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadNativeConfigDefaultsToOptionalServicesDisabled(t *testing.T) {
	clearNativeConfigEnvironment(t)
	cfg, err := LoadNativeConfig()
	if err != nil {
		t.Fatalf("LoadNativeConfig: %v", err)
	}
	if cfg.MinIO.Enabled() || cfg.Transcription.Enabled() {
		t.Fatalf("optional services should be disabled by default: %+v", cfg)
	}
	if cfg.MaxVoiceAudioBytes != 50*1024*1024 {
		t.Fatalf("MaxVoiceAudioBytes = %d", cfg.MaxVoiceAudioBytes)
	}
	if cfg.MobileSyncV1Enabled {
		t.Fatal("mobile_sync_v1 must be disabled by default")
	}
}

func TestLoadNativeConfigParsesMobileSyncV1FeatureFlag(t *testing.T) {
	clearNativeConfigEnvironment(t)
	t.Setenv("FLOWSPACE_ENABLE_MOBILE_SYNC_V1", "true")
	cfg, err := LoadNativeConfig()
	if err != nil {
		t.Fatalf("LoadNativeConfig: %v", err)
	}
	if !cfg.MobileSyncV1Enabled {
		t.Fatal("mobile_sync_v1 feature flag was not enabled")
	}

	t.Setenv("FLOWSPACE_ENABLE_MOBILE_SYNC_V1", "sometimes")
	if _, err := LoadNativeConfig(); err == nil || !strings.Contains(err.Error(), "FLOWSPACE_ENABLE_MOBILE_SYNC_V1") {
		t.Fatalf("invalid mobile sync feature flag error = %v", err)
	}
}

func TestLoadNativeConfigParsesMinIOAndTranscription(t *testing.T) {
	clearNativeConfigEnvironment(t)
	t.Setenv("FLOWSPACE_MINIO_ENDPOINT", "https://minio.example.com:9000")
	t.Setenv("FLOWSPACE_MINIO_ACCESS_KEY", "access")
	t.Setenv("FLOWSPACE_MINIO_SECRET_KEY", "secret")
	t.Setenv("FLOWSPACE_MINIO_BUCKET", "voice")
	t.Setenv("FLOWSPACE_VOICE_MAX_BYTES", "4096")
	t.Setenv("FLOWSPACE_TRANSCRIPTION_URL", "https://speech.example.com/transcribe")
	t.Setenv("FLOWSPACE_TRANSCRIPTION_API_KEY", "speech-key")
	t.Setenv("FLOWSPACE_TRANSCRIPTION_MODEL", "speech-model")
	t.Setenv("FLOWSPACE_TRANSCRIPTION_TIMEOUT_SECONDS", "45")

	cfg, err := LoadNativeConfig()
	if err != nil {
		t.Fatalf("LoadNativeConfig: %v", err)
	}
	if cfg.MinIO.Endpoint != "minio.example.com:9000" || !cfg.MinIO.UseSSL || cfg.MinIO.Bucket != "voice" {
		t.Fatalf("MinIO config = %+v", cfg.MinIO)
	}
	if cfg.Transcription.URL != "https://speech.example.com/transcribe" || cfg.Transcription.Timeout != 45*time.Second {
		t.Fatalf("transcription config = %+v", cfg.Transcription)
	}
	if cfg.MaxVoiceAudioBytes != 4096 {
		t.Fatalf("MaxVoiceAudioBytes = %d", cfg.MaxVoiceAudioBytes)
	}
}

func TestLoadNativeConfigRejectsPartialCredentials(t *testing.T) {
	clearNativeConfigEnvironment(t)
	t.Setenv("FLOWSPACE_MINIO_ENDPOINT", "http://minio.example.com:9000")
	if _, err := LoadNativeConfig(); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("partial MinIO config error = %v", err)
	}

	clearNativeConfigEnvironment(t)
	t.Setenv("FLOWSPACE_TRANSCRIPTION_API_KEY", "orphan-key")
	if _, err := LoadNativeConfig(); err == nil || !strings.Contains(err.Error(), "FLOWSPACE_TRANSCRIPTION_URL") {
		t.Fatalf("partial transcription config error = %v", err)
	}
}

func clearNativeConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"FLOWSPACE_ENABLE_MOBILE_SYNC_V1",
		"FLOWSPACE_VOICE_MAX_BYTES",
		"FLOWSPACE_MINIO_ENDPOINT",
		"FLOWSPACE_MINIO_ACCESS_KEY",
		"FLOWSPACE_MINIO_SECRET_KEY",
		"FLOWSPACE_MINIO_BUCKET",
		"FLOWSPACE_MINIO_REGION",
		"FLOWSPACE_TRANSCRIPTION_URL",
		"FLOWSPACE_TRANSCRIPTION_API_KEY",
		"FLOWSPACE_TRANSCRIPTION_MODEL",
		"FLOWSPACE_TRANSCRIPTION_TIMEOUT_SECONDS",
	} {
		t.Setenv(key, "")
	}
}
