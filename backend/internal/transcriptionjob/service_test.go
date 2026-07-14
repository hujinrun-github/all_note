package transcriptionjob

import "testing"

func TestRequestHashIsStableAndIncludesLanguage(t *testing.T) {
	first, err := RequestHash("voice-id", "zh")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := RequestHash(" voice-id ", " zh ")
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("canonical hash changed: first=%q replay=%q", first, replayed)
	}
	changed, err := RequestHash("voice-id", "en")
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("language must participate in request hash")
	}
}

func TestRetryRequestHashIsStableAndIncludesFailedJob(t *testing.T) {
	first, err := RetryRequestHash("job-a")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := RetryRequestHash(" job-a ")
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("canonical retry hash changed: first=%q replay=%q", first, replayed)
	}
	changed, err := RetryRequestHash("job-b")
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("failed job ID must participate in retry request hash")
	}
}
