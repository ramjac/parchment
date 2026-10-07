package filerepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/presentation"
)

// GetPresentation reads the presentation stored at path.
func (r *Repository) GetPresentation(ctx context.Context, path string) (presentation.Presentation, error) {
	if err := ctx.Err(); err != nil {
		return presentation.Presentation{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return presentation.Presentation{}, err
	}
	content, err := readContent(path, artifact.PresentationKind, presentation.ErrNotFound)
	if err != nil {
		return presentation.Presentation{}, err
	}
	return decodePresentation(path, content)
}

// TransitionPresentation writes target to path only if the file still matches
// expected. A nil expected value creates a new file.
func (r *Repository) TransitionPresentation(ctx context.Context, path string, expected, target *presentation.Presentation) error {
	if target == nil {
		return errors.New("Parchment does not delete artifact files")
	}
	if err := validateTarget(target.Artifact, artifact.PresentationKind); err != nil {
		return err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.update(ctx, path, expected == nil, presentation.ErrNotFound, func(content []byte) ([]byte, error) {
		if expected != nil {
			if err := checkKind(path, content, artifact.PresentationKind); err != nil {
				return nil, err
			}
			current, err := decodePresentation(path, content)
			if err != nil {
				return nil, err
			}
			want := *expected
			want.Path = path
			if !presentation.Equal(current, want) || target.ID != current.ID {
				return nil, changedError(path)
			}
		}
		data, err := presentation.Encode(*target)
		if err != nil {
			return nil, fmt.Errorf("encode presentation: %w", err)
		}
		return data, nil
	})
}

func decodePresentation(path string, content []byte) (presentation.Presentation, error) {
	item, err := presentation.Decode(content)
	if err != nil {
		return presentation.Presentation{}, fmt.Errorf("decode %s: %w", path, err)
	}
	item.Path = path
	return item, nil
}
