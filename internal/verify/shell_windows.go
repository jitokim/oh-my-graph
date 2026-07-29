//go:build windows

package verify

// The interpreter a verification command is run through on Windows. `sh` is not
// on PATH there, so the command line goes to the one interpreter that always is:
// `cmd /c`, whose /c is the same "run this command line and exit" contract as
// sh's -c. Shell syntax differs (a graph written for sh will not run unchanged),
// but that is the graph's problem to state, not a reason for the interpreter to
// be missing.
const (
	defaultShell = "cmd"
	shellFlag    = "/c"
)
