package controlsettings

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/hujinrun/flowspace/internal/outbound"
	"github.com/minio/minio-go/v7"
	miniocredentials "github.com/minio/minio-go/v7/pkg/credentials"
)

type HTTPProber struct {
	client *http.Client
	dialer *outbound.Dialer
}

func NewHTTPProber() (*HTTPProber, error) {
	dialer, err := outbound.NewDialer(nil, outbound.Policy{})
	if err != nil {
		return nil, err
	}
	return &HTTPProber{client: dialer.HTTPClient(), dialer: dialer}, nil
}

func (p *HTTPProber) Probe(ctx context.Context, kind, provider string, configJSON, secret []byte) (ProbeResult, error) {
	var config map[string]any
	if err := json.Unmarshal(configJSON, &config); err != nil {
		return ProbeResult{}, errors.New("invalid profile config")
	}
	endpoint, _ := config["endpoint"].(string)
	if endpoint == "" {
		endpoint, _ = config["base_url"].(string)
	}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return ProbeResult{}, errors.New("service endpoint is required")
	}
	if kind == "data_store" {
		return ProbeResult{}, errors.New("PostgreSQL profile probe is not available yet")
	}
	if err := p.dialer.ValidateURL(ctx, endpoint, "http", "https"); err != nil {
		return ProbeResult{}, err
	}
	if kind == "object_s3" {
		return p.probeObjectStore(ctx, endpoint, config, secret)
	}
	probeURL := endpoint
	directSpeech := kind == "llm_transcription" && (provider == "sensevoice" || provider == "funasr")
	if !directSpeech {
		probeURL += "/models"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	if len(secret) > 0 && kind != "object_s3" {
		request.Header.Set("Authorization", "Bearer "+string(secret))
	}
	response, err := p.client.Do(request)
	if err != nil {
		return ProbeResult{}, err
	}
	defer response.Body.Close()
	_, _ = io.CopyN(io.Discard, response.Body, 4096)
	if directSpeech && response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusNotFound && response.StatusCode < http.StatusInternalServerError {
		return ProbeResult{Code: "OK", Message: "语音服务地址可访问"}, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProbeResult{}, fmt.Errorf("service returned HTTP %d", response.StatusCode)
	}
	return ProbeResult{Code: "OK", Message: "连接测试通过"}, nil
}

func (p *HTTPProber) probeObjectStore(ctx context.Context, endpoint string, config map[string]any, secret []byte) (ProbeResult, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.Path != "" {
		return ProbeResult{}, errors.New("invalid MinIO endpoint")
	}
	var keys objectCredentials
	if json.Unmarshal(secret, &keys) != nil {
		return ProbeResult{}, errors.New("invalid object credentials")
	}
	bucket, _ := config["bucket"].(string)
	region, _ := config["region"].(string)
	client, err := minio.New(parsed.Host, &minio.Options{Creds: miniocredentials.NewStaticV4(keys.AccessKey, keys.SecretKey, ""), Secure: parsed.Scheme == "https", Region: region, Transport: p.client.Transport})
	if err != nil {
		return ProbeResult{}, err
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return ProbeResult{}, err
	}
	if !exists {
		return ProbeResult{}, fmt.Errorf("bucket %s does not exist", bucket)
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return ProbeResult{}, err
	}
	key := fmt.Sprintf(".flowspace-probe/%x", random)
	payload := []byte("flowspace-storage-probe")
	if _, err := client.PutObject(ctx, bucket, key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{ContentType: "text/plain"}); err != nil {
		return ProbeResult{}, err
	}
	defer client.RemoveObject(context.Background(), bucket, key, minio.RemoveObjectOptions{})
	object, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return ProbeResult{}, err
	}
	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		return ProbeResult{}, err
	}
	_ = object.Close()
	if err := client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Code: "OK", Message: "Bucket 读写测试通过"}, nil
}

var _ Prober = (*HTTPProber)(nil)
