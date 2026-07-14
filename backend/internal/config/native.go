package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultMaxVoiceAudioBytes int64 = 50 * 1024 * 1024

type NativeConfig struct {
	MaxVoiceAudioBytes  int64
	MobileSyncV1Enabled bool
	MinIO               MinIOConfig
	Transcription       TranscriptionConfig
}

type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

func (c MinIOConfig) Enabled() bool {
	return strings.TrimSpace(c.Endpoint) != ""
}

type TranscriptionConfig struct {
	URL     string
	APIKey  string
	Model   string
	Timeout time.Duration
}

func (c TranscriptionConfig) Enabled() bool {
	return strings.TrimSpace(c.URL) != ""
}

func LoadNativeConfig() (NativeConfig, error) {
	mobileSyncV1Enabled, err := envBool("FLOWSPACE_ENABLE_MOBILE_SYNC_V1", false)
	if err != nil {
		return NativeConfig{}, err
	}
	maxBytes := defaultMaxVoiceAudioBytes
	if value := strings.TrimSpace(os.Getenv("FLOWSPACE_VOICE_MAX_BYTES")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return NativeConfig{}, errors.New("FLOWSPACE_VOICE_MAX_BYTES must be a positive integer")
		}
		maxBytes = parsed
	}

	minioCfg, err := loadMinIOConfig()
	if err != nil {
		return NativeConfig{}, err
	}
	transcriptionCfg, err := loadTranscriptionConfig()
	if err != nil {
		return NativeConfig{}, err
	}
	return NativeConfig{
		MaxVoiceAudioBytes:  maxBytes,
		MobileSyncV1Enabled: mobileSyncV1Enabled,
		MinIO:               minioCfg,
		Transcription:       transcriptionCfg,
	}, nil
}

func loadMinIOConfig() (MinIOConfig, error) {
	endpoint := strings.TrimSpace(os.Getenv("FLOWSPACE_MINIO_ENDPOINT"))
	accessKey := strings.TrimSpace(os.Getenv("FLOWSPACE_MINIO_ACCESS_KEY"))
	secretKey := strings.TrimSpace(os.Getenv("FLOWSPACE_MINIO_SECRET_KEY"))
	if endpoint == "" && accessKey == "" && secretKey == "" {
		return MinIOConfig{}, nil
	}
	if endpoint == "" || accessKey == "" || secretKey == "" {
		return MinIOConfig{}, errors.New("FLOWSPACE_MINIO_ENDPOINT, FLOWSPACE_MINIO_ACCESS_KEY, and FLOWSPACE_MINIO_SECRET_KEY must be set together")
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return MinIOConfig{}, errors.New("FLOWSPACE_MINIO_ENDPOINT must be an http or https URL")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return MinIOConfig{}, errors.New("FLOWSPACE_MINIO_ENDPOINT must not include a path")
	}
	bucket := strings.TrimSpace(os.Getenv("FLOWSPACE_MINIO_BUCKET"))
	if bucket == "" {
		bucket = "flowspace"
	}
	return MinIOConfig{
		Endpoint:  parsed.Host,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Bucket:    bucket,
		Region:    strings.TrimSpace(os.Getenv("FLOWSPACE_MINIO_REGION")),
		UseSSL:    parsed.Scheme == "https",
	}, nil
}

func loadTranscriptionConfig() (TranscriptionConfig, error) {
	endpoint := strings.TrimSpace(os.Getenv("FLOWSPACE_TRANSCRIPTION_URL"))
	apiKey := strings.TrimSpace(os.Getenv("FLOWSPACE_TRANSCRIPTION_API_KEY"))
	model := strings.TrimSpace(os.Getenv("FLOWSPACE_TRANSCRIPTION_MODEL"))
	if endpoint == "" && (apiKey != "" || model != "") {
		return TranscriptionConfig{}, errors.New("FLOWSPACE_TRANSCRIPTION_URL is required when transcription credentials or model are configured")
	}
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return TranscriptionConfig{}, errors.New("FLOWSPACE_TRANSCRIPTION_URL must be an http or https URL")
		}
	}
	timeout := 2 * time.Minute
	if value := strings.TrimSpace(os.Getenv("FLOWSPACE_TRANSCRIPTION_TIMEOUT_SECONDS")); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 || seconds > 900 {
			return TranscriptionConfig{}, errors.New("FLOWSPACE_TRANSCRIPTION_TIMEOUT_SECONDS must be between 1 and 900")
		}
		timeout = time.Duration(seconds) * time.Second
	}
	return TranscriptionConfig{
		URL:     endpoint,
		APIKey:  apiKey,
		Model:   model,
		Timeout: timeout,
	}, nil
}
