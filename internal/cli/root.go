package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui"
	"example.com/parchment/internal/workspace"
)

// New creates the file-oriented parchment command.
func New(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "parchment <file>",
		Short:         "Open and edit a Markdown or Parchment file",
		Example:       "  parchment notes.md\n  parchment budget.md",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          openFile,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root
}

func openFile(cmd *cobra.Command, args []string) error {
	abs, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolve file path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("resolve file: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", resolved)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	metadata, metadataErr := artifactfile.ReadMetadataFrom(file)
	closeErr := file.Close()
	if metadataErr != nil && !errors.Is(metadataErr, artifactfile.ErrMetadataMissing) {
		return metadataErr
	}
	if closeErr != nil {
		return fmt.Errorf("close file: %w", closeErr)
	}
	userPath, err := config.UserConfigPath()
	if err != nil {
		return err
	}
	settings, err := config.Load(userPath)
	if err != nil {
		return err
	}
	switch {
	case errors.Is(metadataErr, artifactfile.ErrMetadataMissing):
		repository, err := workspace.OpenMarkdownFile(resolved)
		if err != nil {
			return err
		}
		return tui.Run(cmd.Context(), note.NewService(repository, settings.UndoLimit), repository,
			filepath.Base(resolved), resolved, tui.WithSingleMarkdownFile())
	case metadata.Kind == artifact.NoteKind:
		repository, err := workspace.OpenNoteFile(resolved)
		if err != nil {
			return err
		}
		return tui.Run(cmd.Context(), note.NewService(repository, settings.UndoLimit), repository,
			filepath.Base(resolved), resolved, tui.WithSingleMarkdownFile())
	case metadata.Kind == artifact.DocumentKind:
		repository, err := workspace.OpenDocumentFile(resolved)
		if err != nil {
			return err
		}
		documents, err := repository.ListDocuments(cmd.Context())
		if err != nil {
			return err
		}
		if len(documents) != 1 {
			return fmt.Errorf("expected one document in %s, got %d", resolved, len(documents))
		}
		notes, err := workspace.OpenMarkdownFile(resolved)
		if err != nil {
			return err
		}
		return tui.Run(cmd.Context(), note.NewService(notes, settings.UndoLimit), notes,
			filepath.Base(resolved), resolved, tui.WithDocuments(document.NewService(repository, settings.UndoLimit)),
			tui.WithInitialDocument(documents[0].ID), tui.WithSingleDocumentFile())
	case metadata.Kind == artifact.SpreadsheetKind:
		repository, err := workspace.OpenSpreadsheetFile(resolved)
		if err != nil {
			return err
		}
		books, err := repository.ListSpreadsheets(cmd.Context())
		if err != nil {
			return err
		}
		if len(books) != 1 {
			return fmt.Errorf("expected one spreadsheet in %s, got %d", resolved, len(books))
		}
		return tui.RunSpreadsheet(cmd.Context(), spreadsheet.NewService(repository, settings.UndoLimit), repository, books[0].ID, resolved)
	case metadata.Kind == artifact.PresentationKind:
		repository, err := workspace.OpenPresentationFile(resolved)
		if err != nil {
			return err
		}
		decks, err := repository.ListPresentations(cmd.Context())
		if err != nil {
			return err
		}
		if len(decks) != 1 {
			return fmt.Errorf("expected one presentation in %s, got %d", resolved, len(decks))
		}
		return tui.RunPresentation(cmd.Context(), presentation.NewService(repository, settings.UndoLimit), repository, decks[0].ID, resolved)
	default:
		return fmt.Errorf("interactive editing is not supported for %s files", metadata.Kind)
	}
}
