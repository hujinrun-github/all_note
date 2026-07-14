package transcription

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/config"
)

func TestHTTPTranscriberSendsAudioAndParsesText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Errorf("parse multipart: %v", err)
			http.Error(w, "invalid multipart", http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("open uploaded file: %v", err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			t.Errorf("read uploaded file: %v", err)
		}
		if string(body) != "audio bytes" || header.Filename != "note.m4a" {
			t.Errorf("uploaded file name=%q body=%q", header.Filename, body)
		}
		if r.FormValue("model") != "speech-model" || r.FormValue("language") != "zh" {
			t.Errorf("model=%q language=%q", r.FormValue("model"), r.FormValue("language"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": " 转写完成 "})
	}))
	defer server.Close()

	client := NewHTTPTranscriber(config.TranscriptionConfig{
		URL:     server.URL,
		APIKey:  "test-key",
		Model:   "speech-model",
		Timeout: time.Second,
	})
	text, err := client.Transcribe(context.Background(), Input{
		Audio:       stringsReader("audio bytes"),
		Filename:    "note.m4a",
		ContentType: "audio/mp4",
		Language:    "zh",
	})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if text != "转写完成" {
		t.Fatalf("text = %q, want 转写完成", text)
	}
}

type stringReader struct {
	value  string
	offset int
}

func stringsReader(value string) *stringReader {
	return &stringReader{value: value}
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.value) {
		return 0, io.EOF
	}
	n := copy(p, r.value[r.offset:])
	r.offset += n
	return n, nil
}
