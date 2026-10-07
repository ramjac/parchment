package filerepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"example.com/parchment/internal/recovery"
)

const maxRecoveryFileSize = 128 << 20

// LoadRecovery returns the autosaved draft for the artifact file at path.
func (r *Repository) LoadRecovery(ctx context.Context, path string) (recovery.Draft, bool, error) {
	if err := ctx.Err(); err != nil {
		return recovery.Draft{}, false, err
	}
	file, path, err := r.recoveryFile(path)
	if err != nil {
		return recovery.Draft{}, false, err
	}
	data, err := readRegularFile(file, maxRecoveryFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return recovery.Draft{}, false, nil
	}
	if err != nil {
		return recovery.Draft{}, false, fmt.Errorf("read recovery draft: %w", err)
	}
	var draft recovery.Draft
	if err := json.Unmarshal(data, &draft); err != nil {
		return recovery.Draft{}, false, fmt.Errorf("decode recovery draft: %w", err)
	}
	if draft.Path != path || draft.Kind == "" || draft.UpdatedAt.IsZero() || len(draft.Data) == 0 {
		return recovery.Draft{}, false, fmt.Errorf("invalid recovery draft for %s", path)
	}
	return draft, true, nil
}

// SaveRecovery replaces the autosaved draft for draft.Path.
func (r *Repository) SaveRecovery(ctx context.Context, draft recovery.Draft) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, path, err := r.recoveryFile(draft.Path)
	if err != nil {
		return err
	}
	if draft.Kind == "" || len(draft.Data) == 0 {
		return errors.New("invalid recovery draft")
	}
	draft.Path = path
	if draft.UpdatedAt.IsZero() {
		draft.UpdatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("encode recovery draft: %w", err)
	}
	if len(data) > maxRecoveryFileSize {
		return fmt.Errorf("recovery draft exceeds %d bytes", maxRecoveryFileSize)
	}
	// Saves and deletes share the write lock, and cancellation is checked
	// again once it is held, so a cancelled autosave cannot recreate a draft
	// after the editor has saved or closed and removed it.
	return r.withLock(ctx, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensureDirectory(filepath.Dir(file)); err != nil {
			return fmt.Errorf("create recovery storage: %w", err)
		}
		if err := writeAtomic(ctx, file, data, 0o600); err != nil {
			return fmt.Errorf("save recovery draft: %w", err)
		}
		return nil
	})
}

// DeleteRecovery removes the autosaved draft for path, if any.
func (r *Repository) DeleteRecovery(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, _, err := r.recoveryFile(path)
	if err != nil {
		return err
	}
	return r.withLock(ctx, func() error {
		if err := os.Remove(file); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("remove recovery draft: %w", err)
		}
		return syncDirectory(filepath.Dir(file))
	})
}

// recoveryFile maps an artifact path to its draft file, named by a hash of
// the absolute artifact path.
func (r *Repository) recoveryFile(path string) (string, string, error) {
	if path == "" {
		return "", "", errors.New("recovery draft path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(r.stateDir, "recovery", hex.EncodeToString(sum[:16])+".json"), abs, nil
}
