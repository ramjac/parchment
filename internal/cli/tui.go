package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/tui"
)

func tuiCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "tui <file>",
		Short: "Edit a note or document file interactively",
		Long:  "Opens one note or document file in the interactive editor. A missing file is created; without --kind, the editor asks which kind to create.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFlag, _ := cmd.Flags().GetString("kind")
			path, kind, err := resolveTUITarget(args[0], artifact.Kind(kindFlag))
			if err != nil {
				return err
			}
			repo, settings, err := openStore()
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), tui.Config{
				Path:      path,
				Kind:      kind,
				Title:     defaultTitle(path),
				Notes:     note.NewService(repo, settings.UndoLimit),
				Documents: document.NewService(repo, settings.UndoLimit),
				Recovery:  repo,
			})
		},
	}
	command.Flags().String("kind", "", "kind to create when the file is missing: note or document")
	return command
}

// resolveTUITarget returns the absolute path and, for an existing file, its
// artifact kind. An empty kind means the file is missing and the TUI asks.
func resolveTUITarget(arg string, requested artifact.Kind) (string, artifact.Kind, error) {
	if requested != "" && requested != artifact.NoteKind && requested != artifact.DocumentKind {
		return "", "", fmt.Errorf("--kind must be %q or %q", artifact.NoteKind, artifact.DocumentKind)
	}
	path, err := filepath.Abs(arg)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return path, requested, nil
	} else if err != nil {
		return "", "", err
	}
	kind, err := filerepo.Kind(path)
	if err != nil {
		return "", "", err
	}
	if requested != "" && requested != kind {
		return "", "", fmt.Errorf("%s is a %s, not a %s", path, kind, requested)
	}
	switch kind {
	case artifact.NoteKind, artifact.DocumentKind:
		return path, kind, nil
	default:
		return "", "", fmt.Errorf("%s is a %s; the interactive editor supports notes and documents. Use `parchment %s` commands instead", path, kind, kind)
	}
}
