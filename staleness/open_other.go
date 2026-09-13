//go:build !unix

package staleness

import (
	"fmt"
	"os"
)

func openImage(string) (*os.File, error) {
	return nil, fmt.Errorf("executable snapshots unsupported on this platform")
}
