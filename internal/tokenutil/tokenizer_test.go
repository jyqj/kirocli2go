package tokenutil

import "testing"

func TestCountTextStripsThinkingTags(t *testing.T) {
	withTags := CountText("<thinking>abc</thinking>hello")
	withoutTags := CountText("abchello")
	if withTags != withoutTags {
		t.Fatalf("expected thinking tags to be ignored, got %d vs %d", withTags, withoutTags)
	}
}

func TestCountTextNonEmpty(t *testing.T) {
	if CountText("hello world") <= 0 {
		t.Fatalf("expected positive token count")
	}
}
