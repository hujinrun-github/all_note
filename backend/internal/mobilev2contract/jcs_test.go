package mobilev2contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMTDV2Contract012SharedGoldenVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "mobile-v2", "jcs-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name      string `json:"name"`
		Input     string `json:"input"`
		Canonical string `json:"canonical"`
		SHA256    string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			canonical, err := CanonicalizeJSON([]byte(vector.Input))
			if err != nil {
				t.Fatal(err)
			}
			if string(canonical) != vector.Canonical {
				t.Fatalf("canonical = %s, want %s", canonical, vector.Canonical)
			}
			digest := sha256.Sum256(canonical)
			if got := hex.EncodeToString(digest[:]); got != vector.SHA256 {
				t.Fatalf("SHA-256 = %s, want %s", got, vector.SHA256)
			}
		})
	}
}

func TestMTDV2Contract012JCSCanonicalBytes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "object order unicode null and empty values",
			input: `{ "z": null, "object": {}, "empty": [], "emoji": "😀", "a": "é" }`,
			want:  `{"a":"é","emoji":"😀","empty":[],"object":{},"z":null}`,
		},
		{
			name:  "unicode code points are not normalized",
			input: "{\"composed\":\"é\",\"decomposed\":\"é\"}",
			want:  "{\"composed\":\"é\",\"decomposed\":\"é\"}",
		},
		{
			name:  "ECMAScript binary64 formatting",
			input: `{"negativeZero":-0,"million":1e6,"tiny":1e-7,"threshold":1e-6,"huge":1e20,"scientific":1e21}`,
			want:  `{"huge":100000000000000000000,"million":1000000,"negativeZero":0,"scientific":1e+21,"threshold":0.000001,"tiny":1e-7}`,
		},
		{
			name:  "nested recurrence object",
			input: `{"rule":{"weekdays":[5,1,3],"interval":1},"type":"weekly"}`,
			want:  `{"rule":{"interval":1,"weekdays":[5,1,3]},"type":"weekly"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CanonicalizeJSON([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("canonical bytes = %s, want %s", got, test.want)
			}
		})
	}
}

func TestMTDV2Contract012RejectsDuplicateKeysAndNonFiniteNumbers(t *testing.T) {
	for _, input := range []string{`{"a":1,"a":2}`, `{"value":1e9999}`} {
		if _, err := CanonicalizeJSON([]byte(input)); err == nil {
			t.Fatalf("expected canonicalization to reject %s", input)
		}
	}
}

func TestMTDV2Contract012RequestPageAndManifestDigests(t *testing.T) {
	command := []byte(`{
		"request_digest":"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"forwarded_by_device_client_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"payload":{"field_paths":["title","description"]},
		"command_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"origin_device_client_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"workspace_id":"workspace-1","command_type":"task.update",
		"target":{"entity_id":"task-1","client_id":null},"created_runtime_epoch":"8",
		"expected":{"task_revision":{"source":"exact","value":"6"}},
		"depends_on_command_id":null,"supersedes_command_id":null
	}`)
	digest, err := RequestDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:8e3620f38b54e1e91387b3160aa62843b8d8dc4c97ae920460df111041ac2ba2"; digest != want {
		t.Fatalf("request digest = %s, want %s", digest, want)
	}

	entities := []byte(`[
		{"entity_type":"task","entity_id":"t","client_id":null},
		{"entity_type":"project","entity_id":"p","client_id":null}
	]`)
	page, err := PageChecksum("s", 0, "42", entities)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:65c3bef0fbc940de8d5614bbc1381c265a3a811d8cb514bbeac6cf3ab60d330d"; page != want {
		t.Fatalf("page checksum = %s, want %s", page, want)
	}

	manifest, err := ManifestChecksum("s", "42", "g", []string{
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:9a6552abf47cc3871d6abfa456aa7c7ef2260eec929874eba77ea77550dd42bb"; manifest != want {
		t.Fatalf("manifest checksum = %s, want %s", manifest, want)
	}
}
