package main

import (
	"bytes"
	"testing"
)

func TestPrintVersion(t *testing.T) {
	var buf bytes.Buffer
	printVersion(&buf)
	want := "oh-my-graph " + Version + "\n"
	if got := buf.String(); got != want {
		t.Errorf("printVersion output = %q, want %q", got, want)
	}
}
