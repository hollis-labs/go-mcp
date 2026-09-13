//go:build unix

package staleness

import (
	"os"
	"syscall"
)

func openImage(path string) (*os.File, error) {
	// A selector can change to a FIFO between lookup and open. Do not block
	// before f.Stat has a chance to reject anything except a regular file.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
