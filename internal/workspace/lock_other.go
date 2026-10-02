//go:build !unix && !windows

package workspace

import (
	"context"
	"errors"
	"os"
)

func lockFile(context.Context, *os.File) (func() error, error) {
	return nil, errors.New("inter-process artifact locking is unsupported on this platform")
}
