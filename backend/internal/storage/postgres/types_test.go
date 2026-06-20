package postgres

import (
	"testing"
	"time"
)

func TestTagsJSONStringRoundTrip(t *testing.T) {
	t.Run("round trip trims hashes spaces and duplicates", func(t *testing.T) {
		tags, err := tagsJSONStringToArray(`[" sync ","publish","#work","SYNC","","#"]`)
		if err != nil {
			t.Fatalf("parse tags: %v", err)
		}
		if len(tags) != 3 || tags[0] != "sync" || tags[1] != "publish" || tags[2] != "work" {
			t.Fatalf("unexpected tags: %#v", tags)
		}

		jsonText := tagsArrayToJSONString(tags)
		if jsonText != `["sync","publish","work"]` {
			t.Fatalf("unexpected json: %s", jsonText)
		}
	})

	t.Run("empty input becomes empty array", func(t *testing.T) {
		tags, err := tagsJSONStringToArray("  ")
		if err != nil {
			t.Fatalf("parse tags: %v", err)
		}
		if len(tags) != 0 {
			t.Fatalf("expected no tags, got %#v", tags)
		}
		if got := tagsArrayToJSONString(nil); got != `[]` {
			t.Fatalf("expected empty JSON array, got %s", got)
		}
	})

	t.Run("rejects invalid json", func(t *testing.T) {
		if _, err := tagsJSONStringToArray(`["sync"`); err == nil {
			t.Fatal("expected invalid JSON to fail")
		}
	})

	t.Run("rejects non array json", func(t *testing.T) {
		if _, err := tagsJSONStringToArray(`{"tag":"sync"}`); err == nil {
			t.Fatal("expected non-array JSON to fail")
		}
	})
}

func TestUnixTimeRoundTrip(t *testing.T) {
	t.Run("round trip uses UTC", func(t *testing.T) {
		value := int64(1800000000)
		asTime := unixToTime(value)
		if asTime.Location() != time.UTC {
			t.Fatalf("expected UTC time")
		}
		if got := timeToUnix(asTime); got != value {
			t.Fatalf("expected %d, got %d", value, got)
		}
	})

	t.Run("zero time preserves null compatible zero", func(t *testing.T) {
		if got := timeToUnix(time.Time{}); got != 0 {
			t.Fatalf("expected zero time to encode as 0, got %d", got)
		}
		if got := unixToTime(0); !got.IsZero() {
			t.Fatalf("expected zero Unix value to decode as zero time, got %v", got)
		}
	})
}

func TestJSONObjectStringNormalization(t *testing.T) {
	t.Run("normalizes empty string to object", func(t *testing.T) {
		got, err := normalizeJSONObjectString("  ")
		if err != nil {
			t.Fatalf("normalize object: %v", err)
		}
		if got != `{}` {
			t.Fatalf("expected empty object, got %s", got)
		}
	})

	t.Run("compacts valid object", func(t *testing.T) {
		got, err := normalizeJSONObjectString(`{"enabled": true, "labels": ["a"]}`)
		if err != nil {
			t.Fatalf("normalize object: %v", err)
		}
		if got != `{"enabled":true,"labels":["a"]}` {
			t.Fatalf("unexpected normalized object: %s", got)
		}
	})

	t.Run("rejects non objects", func(t *testing.T) {
		for _, raw := range []string{`null`, `[]`, `"value"`, `123`} {
			if _, err := normalizeJSONObjectString(raw); err == nil {
				t.Fatalf("expected %s to fail", raw)
			}
		}
	})
}
