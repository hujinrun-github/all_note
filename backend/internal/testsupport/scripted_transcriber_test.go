package testsupport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hujinrun/flowspace/internal/transcription"
)

func TestScriptedTranscriberRecordsCallsAndHonorsBarrier(t *testing.T) {
	entered := make(chan TranscriptionCall, 1)
	release := make(chan struct{})
	transcriber := NewScriptedTranscriber(TranscriptionStep{
		Text:    "synthetic transcript",
		Entered: entered,
		Release: release,
	})
	result := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		text, err := transcriber.Transcribe(context.Background(), transcription.Input{
			Audio: strings.NewReader("audio"), Filename: "fixture.m4a", ContentType: "audio/mp4", Language: "zh",
		})
		result <- struct {
			text string
			err  error
		}{text: text, err: err}
	}()
	call := <-entered
	if call.Filename != "fixture.m4a" || call.ContentType != "audio/mp4" || call.Language != "zh" {
		t.Fatalf("recorded call = %+v", call)
	}
	select {
	case got := <-result:
		t.Fatalf("transcriber returned before release: %+v", got)
	default:
	}
	close(release)
	got := <-result
	if got.err != nil || got.text != "synthetic transcript" {
		t.Fatalf("result = %q, %v", got.text, got.err)
	}
	if transcriber.CallCount() != 1 {
		t.Fatalf("call count = %d, want 1", transcriber.CallCount())
	}
}

func TestScriptedTranscriberReportsExhaustion(t *testing.T) {
	transcriber := NewScriptedTranscriber()
	if _, err := transcriber.Transcribe(context.Background(), transcription.Input{}); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("error = %v, want ErrSequenceExhausted", err)
	}
}
