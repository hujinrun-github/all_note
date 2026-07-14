package transcription

import (
	"context"
	"errors"
	"io"
)

var ErrUnavailable = errors.New("transcription service is not configured")

type Input struct {
	Audio       io.Reader
	Filename    string
	ContentType string
	Language    string
}

type Transcriber interface {
	Transcribe(context.Context, Input) (string, error)
}

type UnavailableTranscriber struct{}

func (UnavailableTranscriber) Transcribe(context.Context, Input) (string, error) {
	return "", ErrUnavailable
}
