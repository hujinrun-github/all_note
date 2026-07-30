package handler

import "testing"

func TestParseAttachmentRange(t *testing.T) {
	tests := []struct {
		value      string
		size       int64
		start      int64
		end        int64
		partial    bool
		shouldFail bool
	}{
		{value: "", size: 100, start: 0, end: 99},
		{value: "bytes=10-19", size: 100, start: 10, end: 19, partial: true},
		{value: "bytes=90-", size: 100, start: 90, end: 99, partial: true},
		{value: "bytes=-10", size: 100, start: 90, end: 99, partial: true},
		{value: "bytes=100-101", size: 100, shouldFail: true},
		{value: "bytes=1-2,4-5", size: 100, shouldFail: true},
	}
	for _, test := range tests {
		start, end, partial, err := parseAttachmentRange(test.value, test.size)
		if test.shouldFail {
			if err == nil {
				t.Fatalf("range %q should fail", test.value)
			}
			continue
		}
		if err != nil || start != test.start || end != test.end || partial != test.partial {
			t.Fatalf(
				"range %q = (%d,%d,%v,%v), want (%d,%d,%v,nil)",
				test.value, start, end, partial, err,
				test.start, test.end, test.partial,
			)
		}
	}
}
