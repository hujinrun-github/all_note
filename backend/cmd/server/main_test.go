package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestTaskDomainRuntimeBundleClosesGenerationBeforeTenantResolver(t *testing.T) {
	order := make([]string, 0, 3)
	generationErr := errors.New("generation drain failed")
	bundle := &taskDomainRuntimeBundle{
		generation: orderedCloser{name: "generation", order: &order, err: generationErr},
		tenants:    orderedCloser{name: "tenants", order: &order},
	}
	if err := closeTaskDomainAndControl(bundle, orderedCloser{name: "control", order: &order}); !errors.Is(err, generationErr) {
		t.Fatalf("close error=%v", err)
	}
	if want := []string{"generation", "tenants", "control"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("close order=%v want=%v", order, want)
	}
}

type orderedCloser struct {
	name  string
	order *[]string
	err   error
}

func (c orderedCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}
