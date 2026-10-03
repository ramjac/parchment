package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func lockArtifact(ctx context.Context, artifactsDir, id string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(artifactsDir, ".locks")
	if err := ensureDirectory(lockDir); err != nil {
		return nil, fmt.Errorf("create artifact lock directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(lockDir, id+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open artifact lock: %w", err)
	}
	unlockFile, err := lockFile(ctx, file)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("lock artifact: %w", err), file.Close())
	}
	return func() error {
		return errors.Join(unlockFile(), file.Close())
	}, nil
}
