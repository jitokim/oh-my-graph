//go:build !unix

package conventions

import "os"

// openFlags is a plain read-only open where O_NONBLOCK is not available —
// windows above all, which has no FIFO a path can be swapped for. read's
// fh.Stat check still refuses whatever the handle turns out not to be.
const openFlags = os.O_RDONLY
