//go:build !linux

package setup

import (
	"fmt"
	"runtime"
)

// Run is only supported on Linux hosts.
func Run(opts Options) (*Result, error) {
	return nil, fmt.Errorf("goincus init requires Linux (got %s/%s)", runtime.GOOS, runtime.GOARCH)
}
