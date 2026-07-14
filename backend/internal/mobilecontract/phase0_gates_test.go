package mobilecontract

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
)

type phase0GateSpec struct {
	Version     int    `yaml:"version"`
	Contract    string `yaml:"contract"`
	ContractSHA string `yaml:"contract_sha_file"`
	Gates       []struct {
		ID               string   `yaml:"id"`
		Owner            string   `yaml:"owner"`
		RequiredTests    []string `yaml:"required_tests"`
		RequiredEvidence []string `yaml:"required_evidence"`
		EvaluatorRule    string   `yaml:"evaluator_rule"`
	} `yaml:"gates"`
}

func TestPhase0GateSpecIsCompleteAndMachineReadable(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "quality", "phase0-gates.v1.yaml"))
	if err != nil {
		t.Fatalf("read phase-0 gate spec: %v", err)
	}
	var spec phase0GateSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("decode phase-0 gate spec: %v", err)
	}
	if spec.Version != 1 || spec.Contract != "mobile-v1" || spec.ContractSHA == "" {
		t.Fatalf("unexpected gate header: version=%d contract=%q sha_file=%q", spec.Version, spec.Contract, spec.ContractSHA)
	}
	want := map[string]bool{}
	for i := 1; i <= 12; i++ {
		want[gateID(i)] = false
	}
	for _, gate := range spec.Gates {
		if _, ok := want[gate.ID]; !ok {
			t.Errorf("unknown or duplicate gate %q", gate.ID)
			continue
		}
		if want[gate.ID] {
			t.Errorf("duplicate gate %q", gate.ID)
		}
		want[gate.ID] = true
		if gate.Owner == "" || len(gate.RequiredTests) == 0 || len(gate.RequiredEvidence) == 0 || gate.EvaluatorRule == "" {
			t.Errorf("gate %s must define owner, required_tests, required_evidence, and evaluator_rule", gate.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing gate %s", id)
		}
	}
}

func gateID(number int) string {
	return fmt.Sprintf("G0-%02d", number)
}
