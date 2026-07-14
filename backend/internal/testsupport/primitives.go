package testsupport

import (
	"errors"
	"sync"
	"time"
)

var ErrSequenceExhausted = errors.New("scripted sequence is exhausted")

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NextID() (string, error)
}

type JitterSource interface {
	NextJitter(max time.Duration) (time.Duration, error)
}

type FaultInjector interface {
	Inject(point string) error
}

type ManualClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now}
}

func (c *ManualClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *ManualClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

type SequenceIDGenerator struct {
	mu     sync.Mutex
	values []string
	next   int
}

func NewSequenceIDGenerator(values ...string) *SequenceIDGenerator {
	return &SequenceIDGenerator{values: append([]string(nil), values...)}
}

func (g *SequenceIDGenerator) NextID() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next >= len(g.values) {
		return "", ErrSequenceExhausted
	}
	value := g.values[g.next]
	g.next++
	return value, nil
}

type SequenceJitterSource struct {
	mu     sync.Mutex
	values []time.Duration
	next   int
}

func NewSequenceJitterSource(values ...time.Duration) *SequenceJitterSource {
	return &SequenceJitterSource{values: append([]time.Duration(nil), values...)}
}

func (s *SequenceJitterSource) NextJitter(max time.Duration) (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.values) {
		return 0, ErrSequenceExhausted
	}
	value := s.values[s.next]
	s.next++
	if value < 0 {
		return 0, nil
	}
	if max >= 0 && value > max {
		return max, nil
	}
	return value, nil
}

type ScriptedFaultInjector struct {
	mu       sync.Mutex
	steps    map[string][]error
	position map[string]int
	calls    map[string]int
}

func NewScriptedFaultInjector(steps map[string][]error) *ScriptedFaultInjector {
	copySteps := make(map[string][]error, len(steps))
	for point, values := range steps {
		copySteps[point] = append([]error(nil), values...)
	}
	return &ScriptedFaultInjector{
		steps:    copySteps,
		position: make(map[string]int),
		calls:    make(map[string]int),
	}
}

func (i *ScriptedFaultInjector) Inject(point string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls[point]++
	position := i.position[point]
	steps := i.steps[point]
	if position >= len(steps) {
		return nil
	}
	i.position[point] = position + 1
	return steps[position]
}

func (i *ScriptedFaultInjector) CallCount(point string) int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls[point]
}
