package transcription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
)

const maxTranscriptionResponseBytes = 1024 * 1024

type HTTPTranscriber struct {
	url    string
	apiKey string
	model  string
	client *http.Client
}

func NewHTTPTranscriber(cfg config.TranscriptionConfig) *HTTPTranscriber {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &HTTPTranscriber{
		url:    cfg.URL,
		apiKey: cfg.APIKey,
		model:  cfg.Model,
		client: &http.Client{Timeout: timeout},
	}
}

func (t *HTTPTranscriber) Transcribe(ctx context.Context, input Input) (string, error) {
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	writeDone := make(chan error, 1)
	go func() {
		err := writeTranscriptionMultipart(writer, input, t.model)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		_ = pipeWriter.CloseWithError(err)
		writeDone <- err
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, pipeReader)
	if err != nil {
		_ = pipeReader.Close()
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}
	response, err := t.client.Do(req)
	if err != nil {
		_ = pipeReader.Close()
		return "", fmt.Errorf("transcription request failed: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTranscriptionResponseBytes+1))
	closeErr := response.Body.Close()
	writeErr := <-writeDone
	if writeErr != nil {
		return "", fmt.Errorf("stream transcription audio: %w", writeErr)
	}
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close transcription response: %w", closeErr)
	}
	if len(body) > maxTranscriptionResponseBytes {
		return "", errors.New("transcription response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("transcription service returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode transcription response: %w", err)
	}
	payload.Text = strings.TrimSpace(payload.Text)
	if payload.Text == "" {
		return "", errors.New("transcription response did not contain text")
	}
	return payload.Text, nil
}

func writeTranscriptionMultipart(writer *multipart.Writer, input Input, model string) error {
	filename := filepath.Base(strings.TrimSpace(input.Filename))
	if filename == "" || filename == "." {
		filename = "recording.m4a"
	}
	contentType := strings.TrimSpace(input.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, input.Audio); err != nil {
		return err
	}
	if strings.TrimSpace(model) != "" {
		if err := writer.WriteField("model", strings.TrimSpace(model)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(input.Language) != "" {
		if err := writer.WriteField("language", strings.TrimSpace(input.Language)); err != nil {
			return err
		}
	}
	return nil
}
