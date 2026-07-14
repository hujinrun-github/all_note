package testsupport

import (
	"errors"
	"testing"
	"time"
)

func TestManualClockAndSequenceSourcesAreDeterministic(t *testing.T) {
	start := time.Date(2026, time.July, 14, 10, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	clock.Advance(90 * time.Second)
	if got := clock.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("advanced Now() = %v", got)
	}

	ids := NewSequenceIDGenerator("id-1", "id-2")
	for index, want := range []string{"id-1", "id-2"} {
		got, err := ids.NextID()
		if err != nil || got != want {
			t.Fatalf("ID %d = %q, %v; want %q", index, got, err, want)
		}
	}
	if _, err := ids.NextID(); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("exhausted ID error = %v", err)
	}

	jitter := NewSequenceJitterSource(5*time.Second, 20*time.Second, -time.Second)
	for index, want := range []time.Duration{5 * time.Second, 10 * time.Second, 0} {
		got, err := jitter.NextJitter(10 * time.Second)
		if err != nil || got != want {
			t.Fatalf("jitter %d = %v, %v; want %v", index, got, err, want)
		}
	}
	if _, err := jitter.NextJitter(time.Second); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("exhausted jitter error = %v", err)
	}
}

func TestScriptedFaultInjectorConsumesFailuresByPoint(t *testing.T) {
	first := errors.New("first failure")
	injector := NewScriptedFaultInjector(map[string][]error{
		"worker.claim": {first, nil},
	})
	if err := injector.Inject("worker.claim"); !errors.Is(err, first) {
		t.Fatalf("first injection = %v", err)
	}
	if err := injector.Inject("worker.claim"); err != nil {
		t.Fatalf("second injection = %v, want nil", err)
	}
	if err := injector.Inject("worker.claim"); err != nil {
		t.Fatalf("exhausted point should be a no-op, got %v", err)
	}
	if err := injector.Inject("unknown"); err != nil {
		t.Fatalf("unknown point should be a no-op, got %v", err)
	}
	if got := injector.CallCount("worker.claim"); got != 3 {
		t.Fatalf("call count = %d, want 3", got)
	}
}
