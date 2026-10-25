package remote

import (
	"strings"
	"testing"
)

func TestSummarizeReasonIsBounded(t *testing.T) {
	got := SummarizeReason(strings.Repeat("é", 400))
	if len(got) > maxReasonBytes+len(" …") || !strings.HasSuffix(got, " …") {
		t.Errorf("reason of %d bytes not bounded: %q", len(got), got)
	}
	if strings.ContainsRune(strings.TrimSuffix(got, " …"), '�') {
		t.Error("the cut split a rune")
	}
}
