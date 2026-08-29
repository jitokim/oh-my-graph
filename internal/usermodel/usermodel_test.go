package usermodel_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/usermodel"
)

// writeSettings puts a settings document in a temp dir and returns its path.
// A temp dir, never the real home: this package reads the operator's own file,
// and a test that read it would pass or fail by whose machine ran it.
func writeSettings(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), usermodel.SettingsFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

func TestRead_KeyPresentIsReturnedVerbatim(t *testing.T) {
	// The bracketed variant is the point: nothing normalises, case-folds or
	// strips it. The operator's string is the payload.
	path := writeSettings(t, `{"model":"opus[1m]","permissions":{"allow":["Bash(*)"]}}`)

	model, err := usermodel.Read(path)
	if err != nil {
		t.Fatalf("Read: unexpected error: %v", err)
	}
	if model != "opus[1m]" {
		t.Fatalf("model = %q, want %q", model, "opus[1m]")
	}
}

func TestRead_KeyAbsentExpressesNoChoice(t *testing.T) {
	path := writeSettings(t, `{"permissions":{"allow":["Bash(*)"]},"env":{"TOKEN":"secret"}}`)

	model, err := usermodel.Read(path)
	if err != nil {
		t.Fatalf("Read: unexpected error: %v", err)
	}
	if model != "" {
		t.Fatalf("model = %q, want %q (the CLI's own default must stand)", model, "")
	}
}

func TestRead_BlankValueIsTreatedAsAbsent(t *testing.T) {
	// The CLI rejects an empty --model value outright, so emitting one would
	// turn a config typo into a dead run.
	for _, value := range []string{`""`, `"   "`, `"\t"`} {
		path := writeSettings(t, `{"model":`+value+`}`)

		model, err := usermodel.Read(path)
		if err != nil {
			t.Fatalf("Read(%s): unexpected error: %v", value, err)
		}
		if model != "" {
			t.Fatalf("Read(%s) = %q, want %q", value, model, "")
		}
	}
}

func TestRead_FileAbsentIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), usermodel.SettingsFileName)

	model, err := usermodel.Read(path)
	if err != nil {
		t.Fatalf("Read: a machine with no settings file is a supported machine, got error: %v", err)
	}
	if model != "" {
		t.Fatalf("model = %q, want %q", model, "")
	}
}

func TestRead_EmptyPathReadsNothing(t *testing.T) {
	model, err := usermodel.Read("")
	if err != nil {
		t.Fatalf("Read(\"\"): unexpected error: %v", err)
	}
	if model != "" {
		t.Fatalf("model = %q, want %q", model, "")
	}
}

func TestRead_MalformedFileWarnsAndNamesThePathOnly(t *testing.T) {
	// A document whose `model` is the right key at the wrong type, plus a
	// credential in the same file that must not reach the message.
	path := writeSettings(t, `{"model":{"name":"opus"},"env":{"TOKEN":"super-secret-value"}}`)

	model, err := usermodel.Read(path)
	if err == nil {
		t.Fatal("Read: want an error for a malformed settings file, got nil")
	}
	if model != "" {
		t.Fatalf("model = %q, want %q — a malformed file emits no flag", model, "")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name the path %q", err, path)
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("error %q leaks file contents", err)
	}
}

func TestRead_SyntaxErrorIsTheSameCase(t *testing.T) {
	path := writeSettings(t, `{"model": "opus"`)

	model, err := usermodel.Read(path)
	if err == nil {
		t.Fatal("Read: want an error for unparseable JSON, got nil")
	}
	if model != "" {
		t.Fatalf("model = %q, want %q", model, "")
	}
}

func TestRead_UnreadableFileWarnsRatherThanReturningAModel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes do not deny reads on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0o000 file regardless of its mode")
	}
	path := writeSettings(t, `{"model":"opus"}`)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	model, err := usermodel.Read(path)
	if err == nil {
		t.Fatal("Read: want an error for an unreadable settings file, got nil")
	}
	if model != "" {
		t.Fatalf("model = %q, want %q", model, "")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not name the path %q", err, path)
	}
}

func TestDefaultPath_PrefersTheConfigDirEnvOverHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	if got, want := usermodel.DefaultPath(), filepath.Join(dir, usermodel.SettingsFileName); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_FallsBackToTheClaudeHomeDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir does not read $HOME on windows")
	}
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", home)

	if got, want := usermodel.DefaultPath(), filepath.Join(home, ".claude", usermodel.SettingsFileName); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestDefaultPath_UnresolvableHomeIsEmptyNotAGuess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir does not read $HOME on windows")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("HOME", "")

	if got := usermodel.DefaultPath(); got != "" {
		t.Fatalf("DefaultPath() = %q, want %q", got, "")
	}
}
