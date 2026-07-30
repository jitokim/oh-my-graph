//go:build unix

package verify

// The interpreter a verification command is run through on unix/darwin. `sh -c`
// takes the whole command line as ONE argument, so a graph can write an ordinary
// command line (pipes, &&, quoting) instead of an argv array.
const (
	defaultShell = "sh"
	shellFlag    = "-c"
)
