package main

import (
	"fmt"
	"io"
)

// Version is the oh-my-graph release version printed by `oh-my-graph version`.
const Version = "0.3.1"

// printVersion writes the version line to w. Split from the dispatch switch so
// the output is testable without capturing os.Stdout.
func printVersion(w io.Writer) {
	fmt.Fprintf(w, "oh-my-graph %s\n", Version)
}
