package filerepo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"example.com/parchment/internal/imageedit"
)

var _ imageedit.Repository = (*Repository)(nil)

// ImageFormat reads only the signature of an explicitly named regular file.
func ImageFormat(path string) (string, error) {
	_, resolved, err := imagePath(path)
	if err != nil {
		return "", err
	}
	file, err := openRegularFile(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var header [8]byte
	n, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	return imageedit.SniffFormat(header[:n]), nil
}

type imageSnapshot struct {
	path     string
	resolved string
	info     os.FileInfo
	digest   [sha256.Size]byte
}

type imageDestination struct {
	source   *imageSnapshot
	path     string
	resolved string
	prior    *imageSnapshot
}

func imagePath(path string) (string, string, error) {
	if path == "" {
		return "", "", errors.New("image path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	// A dangling final symlink is not a request to create a different file.
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if _, err := filepath.EvalSymlinks(abs); err != nil {
			return "", "", fmt.Errorf("resolve image symlink: %w", err)
		}
	}
	resolved, err := resolve(abs)
	return abs, resolved, err
}

func inspectImage(ctx context.Context, path string, read bool) ([]byte, *imageSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	abs, resolved, err := imagePath(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := openRegularFile(resolved)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if read && info.Size() > imageedit.MaxInputBytes {
		return nil, nil, errors.New("image input exceeds 128 MiB")
	}
	hash := sha256.New()
	var data []byte
	if read {
		data, err = io.ReadAll(io.LimitReader(file, imageedit.MaxInputBytes+1))
		if err == nil && len(data) > imageedit.MaxInputBytes {
			err = errors.New("image input exceeds 128 MiB")
		}
		if err == nil {
			_, err = hash.Write(data)
		}
	} else {
		_, err = io.Copy(hash, file)
	}
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return nil, nil, changedError(path)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return data, &imageSnapshot{path: abs, resolved: resolved, info: info, digest: digest}, nil
}

func (r *Repository) ReadImage(ctx context.Context, path string) ([]byte, any, error) {
	data, snapshot, err := inspectImage(ctx, path, true)
	return data, snapshot, err
}

func checkImageSnapshot(ctx context.Context, expected *imageSnapshot) error {
	_, current, err := inspectImage(ctx, expected.path, false)
	if err != nil {
		return fmt.Errorf("%w: %v", changedError(expected.path), err)
	}
	if current.resolved != expected.resolved || !os.SameFile(current.info, expected.info) ||
		current.digest != expected.digest || current.info.Mode().Perm() != expected.info.Mode().Perm() ||
		!current.info.ModTime().Equal(expected.info.ModTime()) {
		return changedError(expected.path)
	}
	return nil
}

func (r *Repository) PrepareImageWrite(ctx context.Context, source any, path string, inPlace, overwrite bool) (any, error) {
	src, ok := source.(*imageSnapshot)
	if !ok || src == nil {
		return nil, errors.New("invalid image source snapshot")
	}
	if inPlace && (path != "" || overwrite) {
		return nil, errors.New("in-place saving cannot specify an output or force")
	}
	if err := checkImageSnapshot(ctx, src); err != nil {
		return nil, err
	}
	if inPlace {
		return &imageDestination{source: src, path: src.path, resolved: src.resolved, prior: src}, nil
	}
	abs, resolved, err := imagePath(path)
	if err != nil {
		return nil, err
	}
	if resolved == src.resolved {
		return nil, errors.New("output aliases the source image; use --in-place")
	}
	_, prior, err := inspectImage(ctx, abs, false)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if os.SameFile(prior.info, src.info) {
			return nil, errors.New("output aliases the source image; use --in-place")
		}
		if !overwrite {
			return nil, fmt.Errorf("image output %q already exists: %w", path, fs.ErrExist)
		}
	}
	return &imageDestination{source: src, path: abs, resolved: resolved, prior: prior}, nil
}

func (r *Repository) WriteImage(ctx context.Context, source, destination any, data []byte) error {
	_, err := r.writeImage(ctx, source, destination, data, false)
	return err
}

func (r *Repository) WriteImageSnapshot(ctx context.Context, source, destination any, data []byte) (any, error) {
	return r.writeImage(ctx, source, destination, data, true)
}

func (r *Repository) writeImage(ctx context.Context, source, destination any, data []byte, refresh bool) (any, error) {
	src, sourceOK := source.(*imageSnapshot)
	dst, destinationOK := destination.(*imageDestination)
	if !sourceOK || src == nil || !destinationOK || dst == nil {
		return nil, errors.New("invalid image save snapshot")
	}
	if dst.source != src {
		return nil, errors.New("save target belongs to another source image")
	}
	var committed *imageSnapshot
	err := r.withLock(ctx, func() error {
		if err := checkImageSnapshot(ctx, src); err != nil {
			return err
		}
		_, resolved, err := imagePath(dst.path)
		if err != nil {
			return err
		}
		if resolved != dst.resolved {
			return changedError(dst.path)
		}
		if dst.prior == nil {
			err = createAtomic(ctx, resolved, data, 0o600)
		} else {
			if err := checkImageSnapshot(ctx, dst.prior); err != nil {
				return err
			}
			err = writeAtomic(ctx, resolved, data, dst.prior.info.Mode().Perm())
		}
		if err == nil && r.backupDir != "" {
			r.createBackup(ctx, dst.path, data)
		}
		if err == nil && refresh {
			// A cancellation after commit must not masquerade as an unwritten file.
			_, committed, err = inspectImage(context.WithoutCancel(ctx), dst.path, false)
			if err == nil && (committed.resolved != dst.resolved || committed.digest != sha256.Sum256(data)) {
				err = changedError(dst.path)
			}
			if err != nil {
				return fmt.Errorf("image was written, but its saved snapshot could not be verified: %w", err)
			}
		}
		return err
	})
	return committed, err
}
