package mobilecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSHAIsCurrent(t *testing.T) {
	contract, err := os.ReadFile(filepath.Join("..", "..", "api", "mobile-v1.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	checksum, err := os.ReadFile(filepath.Join("..", "..", "api", "mobile-v1.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contract)
	want := hex.EncodeToString(digest[:])
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || fields[0] != want || fields[1] != "mobile-v1.openapi.yaml" {
		t.Fatalf("mobile-v1.sha256 is stale: got %q, want %s  mobile-v1.openapi.yaml", strings.TrimSpace(string(checksum)), want)
	}
}

func TestBackendMakefileExposesStableQualityTargets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(data)
	for _, target := range []string{"test-unit", "test-race", "test-contract-postgres", "test-object-minio", "test-openapi", "test-phase0"} {
		if !strings.Contains(makefile, "\n"+target+":") {
			t.Errorf("backend Makefile is missing stable target %s", target)
		}
	}
}

func TestPullRequestWorkflowRunsRequiredIntegrationTargets(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "test.yml"))
	if err != nil {
		t.Fatalf("read test workflow: %v", err)
	}
	workflow := string(data)
	for _, required := range []string{
		"pull_request:",
		"workflow_call:",
		"FLOWSPACE_REQUIRE_POSTGRES_TESTS: 'true'",
		"FLOWSPACE_REQUIRE_MINIO_TESTS: 'true'",
		"make test-contract-postgres",
		"make test-object-minio",
		"make test-openapi",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("test workflow is missing %q", required)
		}
	}
}

func TestProductionDeployDependsOnReusableTests(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "deploy.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "test:\n    uses: ./.github/workflows/test.yml") {
		t.Error("deploy workflow must invoke the reusable test workflow")
	}
	if !strings.Contains(workflow, "deploy:\n    needs: test") {
		t.Error("production deploy must depend on successful tests")
	}
}
