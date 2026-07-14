package testsupport

import (
	"context"
	"sync"

	"github.com/hujinrun/flowspace/internal/transcription"
)

type TranscriptionCall struct {
	Filename    string
	ContentType string
	Language    string
}

type TranscriptionStep struct {
	Text    string
	Err     error
	Entered chan<- TranscriptionCall
	Release <-chan struct{}
}

type ScriptedTranscriber struct {
	mu       sync.Mutex
	steps    []TranscriptionStep
	position int
	calls    []TranscriptionCall
}

func NewScriptedTranscriber(steps ...TranscriptionStep) *ScriptedTranscriber {
	return &ScriptedTranscriber{steps: append([]TranscriptionStep(nil), steps...)}
}

func (t *ScriptedTranscriber) Transcribe(ctx context.Context, input transcription.Input) (string, error) {
	call := TranscriptionCall{Filename: input.Filename, ContentType: input.ContentType, Language: input.Language}
	t.mu.Lock()
	t.calls = append(t.calls, call)
	if t.position >= len(t.steps) {
		t.mu.Unlock()
		return "", ErrSequenceExhausted
	}
	step := t.steps[t.position]
	t.position++
	t.mu.Unlock()

	if step.Entered != nil {
		select {
		case step.Entered <- call:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if step.Release != nil {
		select {
		case <-step.Release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return step.Text, step.Err
}

func (t *ScriptedTranscriber) CallCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func (t *ScriptedTranscriber) Calls() []TranscriptionCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TranscriptionCall(nil), t.calls...)
}
