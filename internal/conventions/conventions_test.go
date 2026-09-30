package conventions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoad_PrefixShape is ADR 0041 §5 test 2's unit half: the header, each
// file under its ordinal and basename, the separator — and no path, no hash.
func TestLoad_PrefixShape(t *testing.T) {
	dir := t.TempDir()
	style := writeFile(t, dir, "style.md", "use tabs\n")
	testing_ := writeFile(t, dir, "testing.md", "table-driven tests")

	set, err := Load([]string{style, testing_})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := "The person who launched this run gave these conventions for every node. Follow them.\n\n" +
		"## Conventions 1/2: style.md\nuse tabs\n\n" +
		"## Conventions 2/2: testing.md\ntable-driven tests\n\n" +
		"---\n\n"
	if got := string(set.Staged); got != want {
		t.Errorf("prefix:\n%q\nwant:\n%q", got, want)
	}
	for _, src := range set.Sources {
		if strings.Contains(string(set.Staged), src.Path) || strings.Contains(string(set.Staged), src.SHA256) {
			t.Errorf("the prefix carries %s's path or hash", src.Path)
		}
		if !filepath.IsAbs(src.Path) {
			t.Errorf("source path %q is not absolute", src.Path)
		}
	}
	if set.TotalBytes() != len("use tabs\n")+len("table-driven tests") {
		t.Errorf("TotalBytes = %d", set.TotalBytes())
	}
}

// TestLoad_MotivatingCorpusFits is test 8: five files totalling 70 KiB.
func TestLoad_MotivatingCorpusFits(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 5; i++ {
		paths = append(paths, writeFile(t, dir, "doc"+string(rune('a'+i))+".md", strings.Repeat("x", 14*1024)))
	}
	set, err := Load(paths)
	if err != nil {
		t.Fatalf("a 70 KiB five-file corpus must fit: %v", err)
	}
	if len(set.Sources) != 5 || set.TotalBytes() != 70*1024 {
		t.Fatalf("sources = %d, total = %d", len(set.Sources), set.TotalBytes())
	}
	for i, src := range set.Sources {
		if src.Path != paths[i] {
			t.Errorf("source %d = %s, want %s (order is the command line's)", i, src.Path, paths[i])
		}
	}
}

// TestLoad_ExactlyAtTheCapIsAccepted is test 10.
func TestLoad_ExactlyAtTheCapIsAccepted(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.md", strings.Repeat("a", MaxTotalBytes/2))
	b := writeFile(t, dir, "b.md", strings.Repeat("b", MaxTotalBytes/2))
	if _, err := Load([]string{a, b}); err != nil {
		t.Fatalf("exactly %d bytes must be accepted: %v", MaxTotalBytes, err)
	}
}

// TestLoad_Refusals is test 9's unit half: each refusal names the path and the
// reason, never a file's content.
func TestLoad_Refusals(t *testing.T) {
	const secret = "SECRET-CONTENT-MARKER"
	dir := t.TempDir()
	good := writeFile(t, dir, "good.md", secret+" good")
	blank := writeFile(t, dir, "blank.md", "  \n\t\n")
	importOnly := writeFile(t, dir, "CLAUDE.md", "@docs/style.md\n\n  @docs/testing.md\n")
	badUTF8 := writeFile(t, dir, "bad.md", secret+"\xff\xfe")
	half := MaxTotalBytes/2 + 1
	big1 := writeFile(t, dir, "big1.md", strings.Repeat("s", half))
	big2 := writeFile(t, dir, "big2.md", strings.Repeat("s", MaxTotalBytes+1-half))
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		paths     []string
		wantPath  string
		wantParts []string
	}{
		{name: "empty list", paths: nil, wantParts: []string{"no file named"}},
		{name: "missing path", paths: []string{filepath.Join(dir, "nope.md")}, wantPath: "nope.md", wantParts: []string{"cannot be read"}},
		{name: "directory", paths: []string{dir}, wantParts: []string{"is a directory"}},
		{name: "blank file", paths: []string{blank}, wantPath: "blank.md", wantParts: []string{"is blank"}},
		{name: "import-only file", paths: []string{importOnly}, wantPath: "CLAUDE.md",
			wantParts: []string{"@-import", "--conventions docs/style.md --conventions docs/testing.md"}},
		{name: "same file twice", paths: []string{good, good}, wantPath: "good.md", wantParts: []string{"same file"}},
		{name: "same file through a symlink", paths: []string{good, link}, wantPath: "link.md", wantParts: []string{"same file"}},
		{name: "over the cap by one byte", paths: []string{big1, big2},
			wantParts: []string{"131073 bytes", "131072-byte cap", "big1.md 65537 bytes", "big2.md 65536 bytes", "nothing is truncated"}},
		{name: "invalid UTF-8 in the second file", paths: []string{good, badUTF8}, wantPath: "bad.md", wantParts: []string{"not valid UTF-8"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set, err := Load(tc.paths)
			if err == nil {
				t.Fatalf("want a refusal, got a set of %d sources", len(set.Sources))
			}
			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("want *RefusalError, got %T: %v", err, err)
			}
			msg := err.Error()
			if tc.wantPath != "" && !strings.Contains(msg, tc.wantPath) {
				t.Errorf("message does not name %s: %s", tc.wantPath, msg)
			}
			for _, part := range tc.wantParts {
				if !strings.Contains(msg, part) {
					t.Errorf("message lacks %q: %s", part, msg)
				}
			}
			if strings.Contains(msg, secret) {
				t.Errorf("message quotes file content: %s", msg)
			}
		})
	}
}

// TestLoad_MixedFileCountsItsImportLines: a file that mixes text with imports
// is accepted, and the count is what the plan screen prints (§2.5).
func TestLoad_MixedFileCountsItsImportLines(t *testing.T) {
	dir := t.TempDir()
	mixed := writeFile(t, dir, "CLAUDE.md", "# House style\n@docs/style.md\nuse tabs\n@docs/testing.md\n")
	set, err := Load([]string{mixed})
	if err != nil {
		t.Fatalf("a mixed file must be accepted: %v", err)
	}
	if got := set.Sources[0].ImportLines; got != 2 {
		t.Errorf("ImportLines = %d, want 2", got)
	}
	if !strings.Contains(string(set.Staged), "@docs/style.md") {
		t.Error("a mixed file's import lines must reach the node literally")
	}
}

// TestLoad_TemplateSyntaxStaysLiteral: the prefix is never interpolated, so a
// style guide that documents {{ }} keeps it byte for byte (test 3's unit half).
func TestLoad_TemplateSyntaxStaysLiteral(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "tpl.md", "write {{ inputs.x }} and {{ feedback.y }}\n")
	set, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set.Staged), "write {{ inputs.x }} and {{ feedback.y }}\n") {
		t.Errorf("template syntax was altered:\n%s", set.Staged)
	}
}

// TestStage_AndLoadStaged covers the staged copy's round trip and both of
// resume's refusals (§2.6, test 12's unit half).
func TestStage_AndLoadStaged(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "style.md", "use tabs\n")
	set, err := Load([]string{src})
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(dir, "run")
	if err := set.Stage(runDir); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	info, err := os.Stat(filepath.Join(runDir, StagedFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("staged mode = %v, want 0600", info.Mode().Perm())
	}

	got, err := LoadStaged(runDir, set.Sources, set.SHA256())
	if err != nil {
		t.Fatalf("LoadStaged on an untouched copy: %v", err)
	}
	if string(got.Staged) != string(set.Staged) || got.SHA256() != set.SHA256() {
		t.Error("the staged copy did not round-trip")
	}

	t.Run("altered", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(runDir, StagedFileName), []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadStaged(runDir, set.Sources, set.SHA256())
		var mismatch *StagedMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("want *StagedMismatchError, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), set.SHA256()) || !strings.Contains(err.Error(), mismatch.Found) || mismatch.Found == "" {
			t.Errorf("the refusal must name both hashes: %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if err := os.Remove(filepath.Join(runDir, StagedFileName)); err != nil {
			t.Fatal(err)
		}
		_, err := LoadStaged(runDir, set.Sources, set.SHA256())
		var mismatch *StagedMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("want *StagedMismatchError, got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), set.SHA256()) || !strings.Contains(err.Error(), "missing") {
			t.Errorf("the refusal must name the recorded hash and say missing: %v", err)
		}
	})
}
