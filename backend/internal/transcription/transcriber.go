package transcription

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrUnavailable = errors.New("transcription service is not configured")

type Input struct {
	Audio       io.Reader
	Filename    string
	ContentType string
	Language    string
	Timeout     time.Duration
}

type Transcriber interface {
	Transcribe(context.Context, Input) (string, error)
}

type UnavailableTranscriber struct{}

func (UnavailableTranscriber) Transcribe(context.Context, Input) (string, error) {
	return "", ErrUnavailable
}
