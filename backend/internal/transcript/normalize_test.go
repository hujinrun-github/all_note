package transcript

import "testing"

func TestNormalizeVTTDropsCueMetadataAndDeduplicates(t *testing.T) {
	input := "\ufeffWEBVTT\r\n\r\n1\r\n00:00:00.000 --> 00:00:02.000\r\n<v 主持人>欢迎来到节目</v>\r\n\r\n2\r\n00:00:02.000 --> 00:00:04.000\r\n欢迎来到节目\r\n\r\n3\r\n00:00:04.000 --> 00:00:06.000\r\n今天讨论 AI 产品。\r\n"
	got, err := Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "欢迎来到节目\n\n今天讨论 AI 产品。"
	if got != want {
		t.Fatalf("Normalize() = %q, want %q", got, want)
	}
}

func TestNormalizeRejectsEmptyCueFile(t *testing.T) {
	if _, err := Normalize("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\n"); err != ErrEmpty {
		t.Fatalf("error = %v, want ErrEmpty", err)
	}
}
