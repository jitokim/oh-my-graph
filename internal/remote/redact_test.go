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

func TestRedactURLCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:p@ss@host/o/r":                   "https://***@host/o/r",
		"https://ghp_TOKEN@host/o/r":                   "https://***@host/o/r",
		"ssh://git:p@ss@host/o/r":                      "ssh://git:***@host/o/r",
		"git+ssh://git:p@ss@host/o/r":                  "git+ssh://git:***@host/o/r",
		"ftp://me:a@b@c@host/o/r":                      "ftp://me:***@host/o/r",
		"ssh://git@host/o/r":                           "ssh://git@host/o/r",
		"https://host/o/r?x=a@b":                       "https://host/o/r?x=a@b",
		"https://host/o/r#a@b":                         "https://host/o/r#a@b",
		"ssh://host/o/r?x=a:b@c":                       "ssh://host/o/r?x=a:b@c",
		"https://host:8443/o/r":                        "https://host:8443/o/r",
		"https://host/o/r":                             "https://host/o/r",
		"fetch https://u:s1@a/o/r and https://t@b/o/r": "fetch https://***@a/o/r and https://***@b/o/r",
		"ssh://git:s1@a/o/r ssh://git:s@2@b/o/r":       "ssh://git:***@a/o/r ssh://git:***@b/o/r",
	} {
		if got := RedactURLCredentials(in); got != want {
			t.Errorf("RedactURLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}
