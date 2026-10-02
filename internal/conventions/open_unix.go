//go:build unix

package conventions

import (
	"os"
	"syscall"
)

// openFlags opens a conventions file non-blocking. Opening a FIFO for reading
// with O_NONBLOCK returns at once instead of waiting for a writer, so a path
// swapped for one after statOne is refused by read's fh.Stat check rather than
// hanging the launch (#296). O_NONBLOCK changes nothing for a regular file,
// whose reads never block.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK
