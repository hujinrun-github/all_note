package tenantmigration

import (
	"context"
	"strings"
	"testing"
)

func TestVerifyReportsLogicalTableWithoutLeakingContent(t *testing.T) {
	expected, _ := Export(context.Background(), baselineSnapshot(false))
	actualSource := baselineSnapshot(false)
	actualSource.rows["notes"][0]["title"] = "other sensitive title"
	actual, _ := Export(context.Background(), actualSource)
	err := Verify(expected.Manifest, actual.Manifest)
	if err == nil || !strings.Contains(err.Error(), "notes") {
		t.Fatalf("Verify() error=%v", err)
	}
	if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("verification leaked content: %v", err)
	}
}

func TestVerifyRejectsCapabilityAndRevisionMismatch(t *testing.T) {
	expected, _ := Export(context.Background(), baselineSnapshot(false))
	actual := expected.Manifest
	expected.Manifest.Capabilities = map[string]bool{"trigram_search": true}
	actual.Capabilities = map[string]bool{"trigram_search": false}
	if err := Verify(expected.Manifest, actual); err == nil {
		t.Fatal("capability mismatch accepted")
	}
	actual = expected.Manifest
	actual.Tables = append([]TableManifest(nil), expected.Manifest.Tables...)
	actual.Tables[1].MaxRevision++
	if err := Verify(expected.Manifest, actual); err == nil {
		t.Fatal("revision mismatch accepted")
	}
}
