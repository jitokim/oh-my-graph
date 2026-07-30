package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

// isolateRunHome points OMG_HOME at a fresh temp dir so the test's run
// artifacts never touch the real ~/.oh-my-graph. Every test that executes a
// run (or reads run directories) must call this first.
func isolateRunHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMG_HOME", home)
	return home
}

func TestRunsRoot_OMGHomeOverrideWins(t *testing.T) {
	home := isolateRunHome(t)
	if got, want := runsRoot(), filepath.Join(home, "runs"); got != want {
		t.Errorf("runsRoot() = %q, want %q under $OMG_HOME", got, want)
	}
	if got, want := runDirFor("run-1"), filepath.Join(home, "runs", "run-1"); got != want {
		t.Errorf("runDirFor() = %q, want %q under $OMG_HOME", got, want)
	}
}

func TestRunsRoot_DefaultsToDotOhMyGraphUnderUserHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir does not read $HOME on windows")
	}
	fakeHome := t.TempDir()
	t.Setenv("OMG_HOME", "")
	t.Setenv("HOME", fakeHome)
	if got, want := runsRoot(), filepath.Join(fakeHome, ".oh-my-graph", "runs"); got != want {
		t.Errorf("runsRoot() = %q, want %q under the user home", got, want)
	}
}

func TestRunsRoot_FallsBackToRelativeWhenHomeUnresolvable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir does not read $HOME on windows")
	}
	t.Setenv("OMG_HOME", "")
	t.Setenv("HOME", "")
	if got, want := runsRoot(), filepath.Join(".oh-my-graph", "runs"); got != want {
		t.Errorf("runsRoot() = %q, want the relative fallback %q", got, want)
	}
}
