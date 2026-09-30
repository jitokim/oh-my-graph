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

// linuxMaxArgStrlen is Linux's MAX_ARG_STRLEN, 32 pages of 4 KiB: execve
// refuses any single argv string longer than this with E2BIG, and a node's
// whole prompt — prefix included — is one argv string.
const linuxMaxArgStrlen = 32 * 4096

// atCapPair writes a.md and b.md into a fresh dir sized so that their
// rendered prefix is exactly MaxStagedBytes+extra bytes.
func atCapPair(t *testing.T, extra int) []string {
	t.Helper()
	probeDir := t.TempDir()
	probe, err := Load([]string{writeFile(t, probeDir, "a.md", "a"), writeFile(t, probeDir, "b.md", "b")})
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(probe.Staged) - 2
	body := MaxStagedBytes + extra - overhead
	dir := t.TempDir()
	return []string{
		writeFile(t, dir, "a.md", strings.Repeat("a", body/2)),
		writeFile(t, dir, "b.md", strings.Repeat("b", body-body/2)),
	}
}

// TestLoad_ExactlyAtTheCapIsAccepted is test 10: the cap is on the rendered
// prefix, so a prefix of exactly MaxStagedBytes is accepted.
func TestLoad_ExactlyAtTheCapIsAccepted(t *testing.T) {
	set, err := Load(atCapPair(t, 0))
	if err != nil {
		t.Fatalf("a rendered prefix of exactly %d bytes must be accepted: %v", MaxStagedBytes, err)
	}
	if len(set.Staged) != MaxStagedBytes {
		t.Fatalf("len(Staged) = %d, want %d", len(set.Staged), MaxStagedBytes)
	}
}

// TestLoad_LargestAcceptedPrefixFitsOneArgvString: the largest prefix Load
// accepts must leave the node's own prompt room inside Linux's per-argv-string
// limit — a cap on the source files alone let an accepted set fail every
// spawn with E2BIG.
func TestLoad_LargestAcceptedPrefixFitsOneArgvString(t *testing.T) {
	set, err := Load(atCapPair(t, 0))
	if err != nil {
		t.Fatal(err)
	}
	const nodePromptHeadroom = 32 * 1024
	if len(set.Staged)+nodePromptHeadroom > linuxMaxArgStrlen {
		t.Errorf("largest accepted prefix is %d bytes; with %d bytes for the node's prompt it passes the %d-byte argv string limit",
			len(set.Staged), nodePromptHeadroom, linuxMaxArgStrlen)
	}
}

// TestLoad_RenderedPrefixOverTheCapIsRefused: files whose own total fits but
// whose rendered prefix — header, headings, separator — does not are refused.
func TestLoad_RenderedPrefixOverTheCapIsRefused(t *testing.T) {
	paths := atCapPair(t, 1)
	_, err := Load(paths)
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *RefusalError, got %T: %v", err, err)
	}
	for _, part := range []string{"rendered conventions are 98305 bytes", "98304-byte cap", "nothing is truncated"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("message lacks %q: %v", part, err)
		}
	}
}

// TestLoad_OversizedFileIsRefusedWithoutBeingRead: a multi-GB file named by
// mistake is refused from its stat size. The file is sparse, so reading it
// whole would allocate 4 GiB; the test finishing at all is half the assertion.
func TestLoad_OversizedFileIsRefusedWithoutBeingRead(t *testing.T) {
	dir := t.TempDir()
	huge := filepath.Join(dir, "huge.md")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(4 << 30); err != nil {
		f.Close()
		t.Skipf("cannot make a sparse file here: %v", err)
	}
	f.Close()
	small := writeFile(t, dir, "small.md", "use tabs\n")

	_, err = Load([]string{small, huge})
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("want *RefusalError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "huge.md 4294967296 bytes") || !strings.Contains(err.Error(), "98304-byte cap") {
		t.Errorf("message does not name the file's size and the cap: %v", err)
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
	half := MaxStagedBytes/2 + 1
	big1 := writeFile(t, dir, "big1.md", strings.Repeat("s", half))
	big2 := writeFile(t, dir, "big2.md", strings.Repeat("s", MaxStagedBytes+1-half))
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hard.md")
	if err := os.Link(good, hard); err != nil {
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
		{name: "same file through a hard link", paths: []string{good, hard}, wantPath: "hard.md", wantParts: []string{"same file as " + good}},
		{name: "over the cap by one byte", paths: []string{big1, big2},
			wantParts: []string{"98305 bytes", "98304-byte cap", "big1.md 49153 bytes", "big2.md 49152 bytes", "nothing is truncated"}},
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

// TestLoad_AnnotationsAreNotImports: an `@Override`-style line, or a
// path-like one inside a fenced code example, is text — it is not counted,
// and a file made only of annotations is not refused as import-only.
func TestLoad_AnnotationsAreNotImports(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, content string
	}{
		{"bare annotations", "@Override\n@Transactional\n"},
		{"fenced example", "```java\n@Override\n@docs/style.md\n```\n"},
		{"tilde fence", "~~~\n@param\n~~~\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := Load([]string{writeFile(t, dir, strings.ReplaceAll(tc.name, " ", "-")+".md", tc.content)})
			if err != nil {
				t.Fatalf("an annotation-only file must be accepted: %v", err)
			}
			if got := set.Sources[0].ImportLines; got != 0 {
				t.Errorf("ImportLines = %d, want 0", got)
			}
		})
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
	// A staged path that exists but cannot be read is an I/O failure, not a
	// tampered copy: it is returned wrapped, never as a StagedMismatchError.
	t.Run("unreadable", func(t *testing.T) {
		if err := os.Mkdir(filepath.Join(runDir, StagedFileName), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := LoadStaged(runDir, set.Sources, set.SHA256())
		if err == nil {
			t.Fatal("want an error reading a directory as the staged copy")
		}
		var mismatch *StagedMismatchError
		if errors.As(err, &mismatch) {
			t.Fatalf("an unreadable staged copy reported as a hash mismatch: %v", err)
		}
		if !strings.Contains(err.Error(), "read staged conventions") {
			t.Errorf("the error is not wrapped with its context: %v", err)
		}
	})
}

// TestStage_UnwritableRunDirFails: Stage reports, rather than swallows, a run
// directory it cannot write into.
func TestStage_UnwritableRunDirFails(t *testing.T) {
	dir := t.TempDir()
	set, err := Load([]string{writeFile(t, dir, "style.md", "use tabs\n")})
	if err != nil {
		t.Fatal(err)
	}
	blocker := writeFile(t, dir, "run", "a file where the run dir should be")
	if err := set.Stage(blocker); err == nil || !strings.Contains(err.Error(), "stage conventions") {
		t.Fatalf("want a stage conventions error, got %v", err)
	}
}
