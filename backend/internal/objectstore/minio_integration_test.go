package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/testsupport"
)

func TestMinIOAdapterRoundTripAndDeleteIdempotently(t *testing.T) {
	endpoint, ready, err := testsupport.IntegrationTarget("MinIO", "FLOWSPACE_TEST_MINIO_ENDPOINT", "FLOWSPACE_REQUIRE_MINIO_TESTS")
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Skip("FLOWSPACE_TEST_MINIO_ENDPOINT is required for MinIO integration tests")
	}
	accessKey, accessReady, err := testsupport.IntegrationTarget("MinIO", "FLOWSPACE_TEST_MINIO_ACCESS_KEY", "FLOWSPACE_REQUIRE_MINIO_TESTS")
	if err != nil {
		t.Fatal(err)
	}
	secretKey, secretReady, err := testsupport.IntegrationTarget("MinIO", "FLOWSPACE_TEST_MINIO_SECRET_KEY", "FLOWSPACE_REQUIRE_MINIO_TESTS")
	if err != nil {
		t.Fatal(err)
	}
	if !accessReady || !secretReady {
		t.Skip("MinIO integration credentials are not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	bucket := fmt.Sprintf("flowspace-test-%d", time.Now().UnixNano())
	store, err := NewMinIOStore(ctx, config.MinIOConfig{
		Endpoint:  strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://"),
		AccessKey: accessKey,
		SecretKey: secretKey,
		Bucket:    bucket,
		UseSSL:    strings.HasPrefix(endpoint, "https://"),
	})
	if err != nil {
		t.Fatalf("create isolated MinIO store: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = store.client.RemoveBucket(cleanupCtx, bucket)
	})

	payload := []byte("synthetic flowspace minio fixture")
	digest := sha256.Sum256(payload)
	key := "objects/" + hex.EncodeToString(digest[:])
	if err := store.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	object, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	got, readErr := io.ReadAll(object.Body)
	closeErr := object.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read object: read=%v close=%v", readErr, closeErr)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if object.Size != int64(len(payload)) || object.ContentType != "application/octet-stream" {
		t.Fatalf("metadata size=%d content-type=%q", object.Size, object.ContentType)
	}
	if err := store.Remove(ctx, key); err != nil {
		t.Fatalf("remove object: %v", err)
	}
	if err := store.Remove(ctx, key); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
	if _, err := store.Get(ctx, key); err != ErrNotFound {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
}
