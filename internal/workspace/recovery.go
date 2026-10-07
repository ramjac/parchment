package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"example.com/parchment/internal/recovery"
)

const maxRecoveryFileSize = 16 << 20

func (w *Workspace) ListRecovery(ctx context.Context) ([]recovery.Draft, error) {
	dir := filepath.Join(w.root, ".parchment", "recovery")
	if err := ensureRecoveryDirectory(w.root); err != nil {
		return nil, fmt.Errorf("open recovery storage: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list recovery drafts: %w", err)
	}
	drafts := make([]recovery.Draft, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validID.MatchString(id) || !entry.Type().IsRegular() {
			return nil, fmt.Errorf("invalid recovery entry %q", entry.Name())
		}
		data, err := readRegularFile(filepath.Join(dir, entry.Name()), maxRecoveryFileSize)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read recovery draft %s: %w", id, err)
		}
		var draft recovery.Draft
		if err := json.Unmarshal(data, &draft); err != nil {
			return nil, fmt.Errorf("decode recovery draft %s: %w", id, err)
		}
		if draft.ID != id || draft.Kind == "" || draft.UpdatedAt.IsZero() || len(draft.Data) == 0 {
			return nil, fmt.Errorf("invalid recovery draft %s", id)
		}
		drafts = append(drafts, draft)
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].UpdatedAt.After(drafts[j].UpdatedAt)
	})
	return drafts, nil
}

func (w *Workspace) SaveRecovery(ctx context.Context, draft recovery.Draft) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(draft.ID) || draft.Kind == "" || len(draft.Data) == 0 {
		return errors.New("invalid recovery draft")
	}
	if len(draft.Data) > maxRecoveryFileSize {
		return fmt.Errorf("recovery draft exceeds %d bytes", maxRecoveryFileSize)
	}
	if draft.UpdatedAt.IsZero() {
		draft.UpdatedAt = time.Now().UTC()
	}
	dir := filepath.Join(w.root, ".parchment", "recovery")
	if err := ensureRecoveryDirectory(w.root); err != nil {
		return fmt.Errorf("create recovery storage: %w", err)
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return fmt.Errorf("encode recovery draft: %w", err)
	}
	if len(data) > maxRecoveryFileSize {
		return fmt.Errorf("recovery draft exceeds %d bytes", maxRecoveryFileSize)
	}
	lockDir := filepath.Join(w.root, ".parchment")
	if err := withArtifactLock(ctx, lockDir, draft.ID, func() error {
		return writeAtomicContext(ctx, filepath.Join(dir, draft.ID+".json"), data, 0o600)
	}); err != nil {
		return fmt.Errorf("save recovery draft: %w", err)
	}
	return nil
}

func (w *Workspace) DeleteRecovery(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return errors.New("invalid recovery draft ID")
	}
	dir := filepath.Join(w.root, ".parchment", "recovery")
	if err := ensureRecoveryDirectory(w.root); err != nil {
		return fmt.Errorf("open recovery storage: %w", err)
	}
	path := filepath.Join(dir, id+".json")
	lockDir := filepath.Join(w.root, ".parchment")
	if err := withArtifactLock(ctx, lockDir, id, func() error {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect recovery draft: %w", err)
		}
		if !info.Mode().IsRegular() {
			return errors.New("recovery draft is not a regular file")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove recovery draft: %w", err)
		}
		if err := syncDirectory(dir); err != nil {
			return fmt.Errorf("sync recovery storage: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func ensureRecoveryDirectory(root string) error {
	current := root
	for _, name := range []string{".parchment", "recovery"} {
		next := filepath.Join(current, name)
		if err := os.Mkdir(next, 0o700); err == nil {
			if err := syncDirectory(current); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := ensureDirectory(next); err != nil {
			return err
		}
		current = next
	}
	return nil
}

func writeAtomicContext(ctx context.Context, path string, data []byte, mode os.FileMode) (err error) {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".parchment-recovery-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
