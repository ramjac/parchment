package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/presentation"
)

const presentationName = "content.md"

// ListPresentations implements the presentation repository interface.
func (w *Workspace) ListPresentations(ctx context.Context) ([]presentation.Presentation, error) {
	root := w.artifactsPath()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	var items []presentation.Presentation
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !validID.MatchString(entry.Name()) {
			continue
		}
		var item presentation.Presentation
		err := withArtifactLock(ctx, root, entry.Name(), func() error {
			metadata, err := w.readArtifactMetadataUnlocked(entry.Name())
			if errors.Is(err, errNoMetadata) {
				return presentation.ErrNotFound
			}
			if err != nil {
				return err
			}
			if metadata.Kind != artifact.PresentationKind {
				return presentation.ErrNotFound
			}
			var readErr error
			item, readErr = w.readPresentationUnlocked(entry.Name())
			return readErr
		})
		if errors.Is(err, presentation.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

// GetPresentation loads a presentation by stable artifact ID.
func (w *Workspace) GetPresentation(ctx context.Context, id string) (presentation.Presentation, error) {
	if err := ctx.Err(); err != nil {
		return presentation.Presentation{}, err
	}
	if !validID.MatchString(id) {
		return presentation.Presentation{}, presentation.ErrNotFound
	}
	var item presentation.Presentation
	err := withArtifactLock(ctx, w.artifactsPath(), id, func() error {
		var readErr error
		item, readErr = w.readPresentationUnlocked(id)
		return readErr
	})
	return item, err
}

// TransitionPresentation applies a compare-and-swap update to one presentation.
func (w *Workspace) TransitionPresentation(
	ctx context.Context, id string, expected, target *presentation.Presentation,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validID.MatchString(id) {
		return presentation.ErrNotFound
	}
	if expected != nil && expected.ID != id {
		return errors.New("expected presentation ID does not match transition ID")
	}
	if target != nil && target.ID != id {
		return errors.New("target presentation ID does not match transition ID")
	}
	root := w.artifactsPath()
	return withArtifactLock(ctx, root, id, func() error {
		dir := filepath.Join(root, id)
		_, dirErr := os.Lstat(dir)
		if errors.Is(dirErr, os.ErrNotExist) {
			if expected == nil && target != nil {
				return w.savePresentationLocked(ctx, *target)
			}
			return presentation.ErrNotFound
		}
		if dirErr != nil {
			return fmt.Errorf("inspect artifact storage: %w", dirErr)
		}
		current, err := w.readPresentationUnlocked(id)
		if errors.Is(err, presentation.ErrNotFound) {
			if expected == nil {
				return fmt.Errorf("artifact ID %s is occupied by another artifact", id)
			}
			return presentation.ErrNotFound
		}
		if err != nil {
			return err
		}
		if expected == nil {
			return fmt.Errorf("presentation %s already exists", id)
		}
		if !presentation.Equal(current, *expected) {
			return fmt.Errorf("presentation %s changed since this operation was recorded", id)
		}
		if target == nil {
			return w.deleteArtifactLocked(ctx, root, id, func() error {
				deleted, err := w.readPresentationUnlocked(id)
				if err != nil {
					return err
				}
				if !presentation.Equal(deleted, *expected) {
					return fmt.Errorf("presentation %s changed before deletion", id)
				}
				return nil
			})
		}
		return w.savePresentationLocked(ctx, *target)
	})
}

func (w *Workspace) savePresentationLocked(ctx context.Context, item presentation.Presentation) error {
	if !validID.MatchString(item.ID) || item.Kind != artifact.PresentationKind {
		return errors.New("invalid presentation artifact")
	}
	if item.Location != w.ArtifactLocation(item.ID) {
		return errors.New("invalid presentation location")
	}
	data, err := presentation.Encode(item)
	if err != nil {
		return err
	}
	dir := w.artifactPath(item.ID)
	created := false
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create presentation storage: %w", err)
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil {
			return fmt.Errorf("inspect presentation storage: %w", statErr)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("presentation storage path is not a directory")
		}
	} else {
		created = true
		if err := syncDirectory(filepath.Dir(dir)); err != nil {
			_ = os.Remove(dir)
			return fmt.Errorf("sync presentation storage parent: %w", err)
		}
	}
	if err := recoverArtifactFiles(dir); err != nil {
		if created {
			_ = os.Remove(dir)
		}
		return fmt.Errorf("recover presentation files: %w", err)
	}
	if !created {
		if _, err := w.readPresentationUnlocked(item.ID); err != nil {
			if errors.Is(err, presentation.ErrNotFound) {
				return fmt.Errorf("artifact ID %s is occupied by another artifact", item.ID)
			}
			return fmt.Errorf("inspect existing presentation before save: %w", err)
		}
	}
	if err := replaceArtifactFiles(ctx, dir, []stagedArtifactFile{{name: presentationName, data: data}}); err != nil {
		if created {
			if cleanupErr := os.Remove(dir); cleanupErr != nil {
				return fmt.Errorf("save presentation: %w (also failed to remove new artifact directory: %v)", err, cleanupErr)
			}
		}
		return fmt.Errorf("save presentation: %w", err)
	}
	return nil
}

func (w *Workspace) readPresentationUnlocked(id string) (presentation.Presentation, error) {
	dir := w.artifactPath(id)
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return presentation.Presentation{}, presentation.ErrNotFound
		}
		return presentation.Presentation{}, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return presentation.Presentation{}, errors.New("presentation storage path is not a directory")
	}
	data, err := readRegularFile(filepath.Join(dir, presentationName), artifactfile.MaxFileSize)
	if errors.Is(err, os.ErrNotExist) {
		return presentation.Presentation{}, presentation.ErrNotFound
	}
	if err != nil {
		return presentation.Presentation{}, fmt.Errorf("read presentation %s: %w", id, err)
	}
	metadata, err := artifactfile.ReadMetadata(data)
	if err != nil {
		return presentation.Presentation{}, fmt.Errorf("read presentation metadata %s: %w", id, err)
	}
	if metadata.ID != id {
		return presentation.Presentation{}, fmt.Errorf("invalid presentation metadata for %s", id)
	}
	if metadata.Kind != artifact.PresentationKind {
		return presentation.Presentation{}, presentation.ErrNotFound
	}
	item, err := presentation.Decode(data)
	if err != nil {
		return presentation.Presentation{}, fmt.Errorf("decode presentation %s: %w", id, err)
	}
	if item.ID != id || item.Location != w.ArtifactLocation(id) {
		return presentation.Presentation{}, fmt.Errorf("invalid presentation metadata for %s", id)
	}
	return item, nil
}
