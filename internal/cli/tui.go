package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui"
)

var editorKinds = []artifact.Kind{
	artifact.NoteKind, artifact.DocumentKind, artifact.SpreadsheetKind, artifact.PresentationKind,
}

func tuiCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "tui <file>",
		Short: "Edit a file interactively",
		Long: "Opens one note, document, spreadsheet, or presentation file in the interactive editor.\n" +
			"Plain Markdown files open as notes. A missing file is created; without --kind, the\n" +
			"editor asks which kind to create.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, _ := cmd.Flags().GetString("kind")
			return runTUI(cmd, args[0], artifact.Kind(kind), false)
		},
	}
	command.Flags().String("kind", "", "kind to create when the file is missing: note, document, spreadsheet, or presentation")
	return command
}

// runTUI opens arg in the interactive editor. edit opens an existing
// document in its editor instead of its reader.
func runTUI(cmd *cobra.Command, arg string, requested artifact.Kind, edit bool) error {
	path, kind, err := resolveTUITarget(arg, requested)
	if err != nil {
		return err
	}
	repo, settings, err := openStore()
	if err != nil {
		return err
	}
	return tui.Run(cmd.Context(), tui.Config{
		Path:          path,
		Kind:          kind,
		Edit:          edit,
		Notes:         note.NewService(repo, settings.UndoLimit),
		Documents:     document.NewService(repo, settings.UndoLimit),
		Spreadsheets:  spreadsheet.NewService(repo, settings.UndoLimit),
		Presentations: presentation.NewService(repo, settings.UndoLimit),
		Recovery:      repo,
	})
}

// resolveTUITarget returns the absolute path and, for an existing file, its
// artifact kind. Plain Markdown files are notes. An empty kind means the file
// is missing and the editor asks which kind to create.
func resolveTUITarget(arg string, requested artifact.Kind) (string, artifact.Kind, error) {
	if requested != "" && !validEditorKind(requested) {
		return "", "", fmt.Errorf("--kind must be one of %q, %q, %q, or %q", editorKinds[0], editorKinds[1], editorKinds[2], editorKinds[3])
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
	if errors.Is(err, filerepo.ErrNotArtifact) {
		kind, err = artifact.NoteKind, nil
	}
	if err != nil {
		return "", "", err
	}
	if requested != "" && requested != kind {
		return "", "", fmt.Errorf("%s is a %s, not a %s", path, kind, requested)
	}
	if !validEditorKind(kind) {
		return "", "", fmt.Errorf("%s is a %s; the interactive editor cannot open it", path, kind)
	}
	return path, kind, nil
}

func validEditorKind(kind artifact.Kind) bool {
	for _, candidate := range editorKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

// addEditFlag adds --no-edit to a create command.
func addEditFlag(command *cobra.Command) {
	command.Flags().Bool("no-edit", false, "only create the file; do not open it in the editor")
}

// finishCreate opens a newly created file in its editor when Parchment runs
// in an interactive terminal; otherwise, as in scripts and pipelines, it
// prints the file's path.
func finishCreate(cmd *cobra.Command, streams output, path string, kind artifact.Kind) error {
	noEdit, _ := cmd.Flags().GetBool("no-edit")
	if !noEdit && streams.interactive() {
		return runTUI(cmd, path, kind, true)
	}
	_, err := fmt.Fprintf(streams.out, "%s\n", path)
	return err
}

// interactive reports whether standard input and the command's output are
// both terminals.
func (s output) interactive() bool {
	out, ok := s.out.(*os.File)
	return ok && isTerminal(out) && isTerminal(os.Stdin)
}

func isTerminal(file *os.File) bool {
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}
