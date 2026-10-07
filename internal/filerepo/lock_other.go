//go:build !(android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris) && !windows

package filerepo

import (
	"context"
	"errors"
	"os"
)

func lockFile(context.Context, *os.File) (func() error, error) {
	return nil, errors.New("inter-process artifact locking is unsupported on this platform")
}
