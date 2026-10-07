package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInputFile writes body to name inside a fresh temp dir and returns its path.
func writeInputFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// wantInputFileRefusal loads body as name and asserts the refusal names the
// file path and every one of wants.
func wantInputFileRefusal(t *testing.T, name, body string, wants ...string) {
	t.Helper()
	path := writeInputFile(t, name, body)
	got, err := loadInputFile(path)
	if err == nil {
		t.Fatalf("loadInputFile(%s) = %v, want a refusal", body, got)
	}
	for _, want := range append([]string{path}, wants...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestLoadInputFile_BindsYAML_354(t *testing.T) {
	path := writeInputFile(t, "in.yaml", "repo: /tmp/r\nticket: \"ABC-1\"\ncount: 7\nok: true\n")
	got, err := loadInputFile(path)
	if err != nil {
		t.Fatalf("loadInputFile: %v", err)
	}
	want := map[string]string{"repo": "/tmp/r", "ticket": "ABC-1", "count": "7", "ok": "true"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestLoadInputFile_BindsJSON_354(t *testing.T) {
	path := writeInputFile(t, "in.json", `{"repo": "/tmp/r", "count": 7, "ok": false, "eq": "a=b"}`)
	got, err := loadInputFile(path)
	if err != nil {
		t.Fatalf("loadInputFile: %v", err)
	}
	want := map[string]string{"repo": "/tmp/r", "count": "7", "ok": "false", "eq": "a=b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestLoadInputFile_KeepsScalarTextVerbatim_354(t *testing.T) {
	for _, text := range []string{"1.10", "0123", "yes", "1e3"} {
		path := writeInputFile(t, "in.yaml", "v: "+text+"\n")
		got, err := loadInputFile(path)
		if err != nil {
			t.Fatalf("%s: loadInputFile: %v", text, err)
		}
		if got["v"] != text {
			t.Errorf("v = %q, want %q byte-identical", got["v"], text)
		}
	}
}

func TestLoadInputFile_RefusesNestedValue_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "outer:\n  inner: x\n", `"outer"`, "map")
}

func TestLoadInputFile_RefusesListValue_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "items: [a, b]\n", `"items"`, "list")
}

func TestLoadInputFile_RefusesNullValue_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "empty:\n", `"empty"`, "null")
	wantInputFileRefusal(t, "in.json", `{"gone": null}`, `"gone"`, "null")
}

func TestLoadInputFile_RefusesAliasValue_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "a: &x v\nb: *x\n", `"b"`, "alias")
}

func TestLoadInputFile_RefusesNonMapTopLevel_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "- a\n- b\n", "top level is a list")
	wantInputFileRefusal(t, "in.json", `"just text"`, "top level is a scalar")
	wantInputFileRefusal(t, "in.yaml", "", "empty document")
	wantInputFileRefusal(t, "in.yaml", "# only a comment\n", "empty document")
}

func TestLoadInputFile_RefusesDuplicateKeyJSON_354(t *testing.T) {
	wantInputFileRefusal(t, "in.json", `{"repo": "a", "repo": "b"}`, `"repo"`, "more than once")
}

func TestLoadInputFile_RefusesDuplicateKeyYAML_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "repo: a\nrepo: b\n", `"repo"`, "more than once")
}

func TestLoadInputFile_RefusesKeyInputCannotBind_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "\"\": x\n", `key ""`)
	wantInputFileRefusal(t, "in.yaml", "\"a=b\": x\n", `key "a=b"`)
	wantInputFileRefusal(t, "in.yaml", "7: x\n", "not a string")
}

func TestLoadInputFile_RefusesMissingFile_354(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	got, err := loadInputFile(path)
	if err == nil {
		t.Fatalf("loadInputFile(missing) = %v, want a refusal", got)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name %q", err, path)
	}
}

func TestLoadInputFile_RefusesMalformedFile_354(t *testing.T) {
	wantInputFileRefusal(t, "in.json", `{"repo": `, "not valid YAML or JSON")
}

func TestLoadInputFile_RefusesSecondDocument_354(t *testing.T) {
	wantInputFileRefusal(t, "in.yaml", "repo: /x\n---\ntask: do it\n", "more than one YAML document")
	wantInputFileRefusal(t, "in.yaml", "repo: /x\n---\n", "more than one YAML document")
}
