package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOStore struct {
	client *minio.Client
	bucket string
}

func NewMinIOStore(ctx context.Context, cfg config.MinIOConfig) (*MinIOStore, error) {
	return newMinIOStore(ctx, cfg, nil, true)
}

func NewMinIORuntimeStore(ctx context.Context, cfg config.MinIOConfig, transport http.RoundTripper) (*MinIOStore, error) {
	return newMinIOStore(ctx, cfg, transport, false)
}

func newMinIOStore(ctx context.Context, cfg config.MinIOConfig, transport http.RoundTripper, createBucket bool) (*MinIOStore, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.UseSSL, Region: cfg.Region, Transport: transport,
	})
	if err != nil {
		return nil, fmt.Errorf("create MinIO client: %w", err)
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check MinIO bucket %s: %w", cfg.Bucket, err)
	}
	if !exists {
		if !createBucket {
			return nil, fmt.Errorf("MinIO bucket %s does not exist", cfg.Bucket)
		}
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("create MinIO bucket %s: %w", cfg.Bucket, err)
		}
	}
	return &MinIOStore{client: client, bucket: cfg.Bucket}, nil
}

func (s *MinIOStore) Put(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, reader, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

func (s *MinIOStore) Get(ctx context.Context, key string) (*Object, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, mapMinIOError(err)
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, mapMinIOError(err)
	}
	return &Object{
		Body:        object,
		Size:        info.Size,
		ContentType: info.ContentType,
		ETag:        info.ETag,
	}, nil
}

func (s *MinIOStore) Remove(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	return mapMinIOError(err)
}

func mapMinIOError(err error) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.StatusCode == 404 {
		return ErrNotFound
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return err
}
