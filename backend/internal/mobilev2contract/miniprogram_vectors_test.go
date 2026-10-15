package mobilev2contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMTDV2ContractSharedMiniProgramVectors validates the golden vectors that
// are shared with the WeChat Mini Program client. The same file is consumed by
// miniprogram/test/ so both sides agree byte-for-byte on JCS canonicalization,
// semantic-set ordering and digest construction.
//
// If this test fails after a contract change, regenerate the vectors and update
// the Mini Program copy together; never edit one side alone.
func TestMTDV2ContractSharedMiniProgramVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "miniprogram-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name     string          `json:"name"`
		Kind     string          `json:"kind"`
		Input    json.RawMessage `json:"input"`
		Expected string          `json:"expected"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("miniprogram-vectors.json contains no vectors")
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			switch vector.Kind {
			case "jcs":
				var input string
				if err := json.Unmarshal(vector.Input, &input); err != nil {
					t.Fatal(err)
				}
				canonical, err := CanonicalizeJSON([]byte(input))
				if err != nil {
					t.Fatal(err)
				}
				if string(canonical) != vector.Expected {
					t.Fatalf("canonical = %s, want %s", canonical, vector.Expected)
				}
			case "request":
				digest, err := RequestDigest(vector.Input)
				if err != nil {
					t.Fatal(err)
				}
				if digest != vector.Expected {
					t.Fatalf("request digest = %s, want %s", digest, vector.Expected)
				}
			case "page":
				var input struct {
					SnapshotID   string          `json:"snapshot_id"`
					PageIndex    int             `json:"page_index"`
					AsOfSequence string          `json:"as_of_sequence"`
					Entities     json.RawMessage `json:"entities"`
				}
				if err := json.Unmarshal(vector.Input, &input); err != nil {
					t.Fatal(err)
				}
				checksum, err := PageChecksum(input.SnapshotID, input.PageIndex, input.AsOfSequence, input.Entities)
				if err != nil {
					t.Fatal(err)
				}
				if checksum != vector.Expected {
					t.Fatalf("page checksum = %s, want %s", checksum, vector.Expected)
				}
			case "manifest":
				var input struct {
					SnapshotID      string   `json:"snapshot_id"`
					AsOfSequence    string   `json:"as_of_sequence"`
					ScopeGeneration string   `json:"scope_generation"`
					PageChecksums   []string `json:"page_checksums"`
				}
				if err := json.Unmarshal(vector.Input, &input); err != nil {
					t.Fatal(err)
				}
				checksum, err := ManifestChecksum(input.SnapshotID, input.AsOfSequence, input.ScopeGeneration, input.PageChecksums)
				if err != nil {
					t.Fatal(err)
				}
				if checksum != vector.Expected {
					t.Fatalf("manifest checksum = %s, want %s", checksum, vector.Expected)
				}
			default:
				t.Fatalf("unknown vector kind %q", vector.Kind)
			}
		})
	}
}

// TestMTDV2ContractMiniProgramVectorsCoverBothOrderingRules guards the two
// different sort rules that coexist in the contract: object keys sort by UTF-16
// code unit, while semantic sets (entities/identity_mappings/field_paths) sort
// by Unicode code point. U+E000 and U+1F600 swap order between the two rules,
// so they are the discriminating pair.
func TestMTDV2ContractMiniProgramVectorsCoverBothOrderingRules(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "miniprogram-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name     string          `json:"name"`
		Kind     string          `json:"kind"`
		Input    json.RawMessage `json:"input"`
		Expected string          `json:"expected"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, vector := range vectors {
		seen[vector.Name] = true
	}
	for _, name := range []string{
		"jcs-object-key-utf16-order",
		"request-digest-entities-codepoint-order",
	} {
		if !seen[name] {
			t.Fatalf("missing discriminating vector %q; the Mini Program cannot verify both ordering rules without it", name)
		}
	}
}
