package filerepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ramjac/parchment/internal/artifact"
	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/note"
	"github.com/ramjac/parchment/internal/notetext"
)

func (r *Repository) GetText(ctx context.Context, path, encoding string) (note.TextSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return note.TextSnapshot{}, err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return note.TextSnapshot{}, err
	}
	if err := r.recoverHistory(ctx, path); err != nil {
		return note.TextSnapshot{}, err
	}
	resolved, err := resolve(path)
	if err != nil {
		return note.TextSnapshot{}, err
	}
	raw, err := readRegularFile(resolved, artifactfile.MaxFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return note.TextSnapshot{}, fmt.Errorf("%s: %w", path, note.ErrNotFound)
	}
	if err != nil {
		return note.TextSnapshot{}, err
	}
	if !isPlainMarkdown(raw) {
		if encoding != "" && encoding != notetext.Auto {
			return note.TextSnapshot{}, errors.New("--encoding is only supported for existing plain-text notes")
		}
		if err := checkKind(path, raw, artifact.NoteKind); err != nil {
			return note.TextSnapshot{}, err
		}
		n, err := decodeNote(path, raw)
		return note.TextSnapshot{Note: n, Format: notetext.Format{Encoding: notetext.UTF8}, Document: notetext.Document{Text: n.Body}}, err
	}
	doc, format, err := notetext.Decode(raw, encoding)
	if err != nil {
		return note.TextSnapshot{}, fmt.Errorf("open %s: %w; to select an encoding, use parchment tui %q --encoding <encoding>", path, err, path)
	}
	if err := checkPlainText(doc); err != nil {
		return note.TextSnapshot{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return note.TextSnapshot{}, err
	}
	n := note.Note{Artifact: artifact.Artifact{
		Kind: artifact.NoteKind, FormatVersion: artifact.FormatVersion,
		CreatedAt: info.ModTime().UTC(), ModifiedAt: info.ModTime().UTC(),
	}, Body: string(raw)}
	setRuntimeIdentity(&n.Artifact, path)
	return note.TextSnapshot{Note: n, Plain: true, Raw: raw, Format: format, Document: doc}, nil
}

func checkPlainText(doc notetext.Document) error {
	_, err := artifactfile.ReadMetadata([]byte(doc.Text))
	if !errors.Is(err, artifactfile.ErrMetadataMissing) {
		return errors.New("encoded Parchment artifacts are not supported; keep typed artifacts in their original UTF-8 format")
	}
	return nil
}

func (r *Repository) TransitionText(ctx context.Context, path string, expected, target note.TextSnapshot) (note.TextSnapshot, error) {
	if !expected.Plain || !target.Plain || target.Format != expected.Format {
		return note.TextSnapshot{}, errors.New("invalid plain-note text transition")
	}
	data, err := notetext.Encode(target.Document, expected.Format)
	if err != nil {
		return note.TextSnapshot{}, err
	}
	if err := checkPlainText(target.Document); err != nil {
		return note.TextSnapshot{}, err
	}
	if bytes.Equal(data, expected.Raw) {
		err := r.withLock(ctx, func() error {
			resolved, err := resolve(path)
			if err != nil {
				return err
			}
			current, err := readRegularFile(resolved, artifactfile.MaxFileSize)
			if err != nil {
				return err
			}
			if !bytes.Equal(current, expected.Raw) {
				return changedError(path)
			}
			return nil
		})
		return expected.Clone(), err
	}
	err = r.update(ctx, path, false, note.ErrNotFound, func(current []byte) ([]byte, error) {
		if !bytes.Equal(current, expected.Raw) {
			return nil, changedError(path)
		}
		return data, nil
	})
	if err != nil {
		return note.TextSnapshot{}, err
	}
	target = target.Clone()
	target.Raw = append([]byte(nil), data...)
	target.Note.Body = string(data)
	return target, nil
}
