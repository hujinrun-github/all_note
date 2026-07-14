package objectstoretest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/testsupport"
)

func TestFaultingStoreCanFailBeforeOrAfterDelegation(t *testing.T) {
	ctx := context.Background()
	base := objectstore.NewMemoryStore()
	beforeErr := errors.New("before put")
	before := FaultingStore{
		Base:   base,
		Faults: testsupport.NewScriptedFaultInjector(map[string][]error{PutBefore: {beforeErr}}),
	}
	if err := before.Put(ctx, "before", bytes.NewReader([]byte("x")), 1, "text/plain"); !errors.Is(err, beforeErr) {
		t.Fatalf("before error = %v", err)
	}
	if _, err := base.Get(ctx, "before"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("before failure wrote object: %v", err)
	}

	afterErr := errors.New("lost put response")
	after := FaultingStore{
		Base:   base,
		Faults: testsupport.NewScriptedFaultInjector(map[string][]error{PutAfter: {afterErr}}),
	}
	if err := after.Put(ctx, "after", bytes.NewReader([]byte("x")), 1, "text/plain"); !errors.Is(err, afterErr) {
		t.Fatalf("after error = %v", err)
	}
	object, err := base.Get(ctx, "after")
	if err != nil {
		t.Fatalf("after failure should preserve delegated write: %v", err)
	}
	_ = object.Body.Close()
}
