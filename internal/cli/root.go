package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/filerepo"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/spreadsheet"
)

type output struct {
	out io.Writer
	err io.Writer
}

// New creates the parchment command tree.
func New(stdout, stderr io.Writer) *cobra.Command {
	streams := output{out: stdout, err: stderr}
	root := &cobra.Command{
		Use:   "parchment [file]",
		Short: "Local-first notes, documents, spreadsheets, and presentations",
		Long: "Each Parchment artifact is an ordinary Markdown file that you name and place anywhere.\n" +
			"`parchment <file>` opens the file in the interactive editor, like `parchment tui <file>`.\n" +
			"Plain Markdown files open as notes and stay plain Markdown.\n" +
			"Configuration is read from ~/.parchment/parchment.toml, which is created on first use.",
		Example:       "  parchment ~/Documents/ideas.md\n  parchment note create ~/Documents/ideas.md\n  parchment note show ~/Documents/ideas.md",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return runTUI(cmd, args[0], "")
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	notes := &cobra.Command{Use: "note", Short: "Create and manage Markdown notes"}
	root.AddCommand(groupCommand(notes))
	create := &cobra.Command{
		Use: "create <file>", Short: "Create a note file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			body, _ := cmd.Flags().GetString("body")
			n, err := service.Create(cmd.Context(), path, body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", n.Path)
			return err
		},
	}
	create.Flags().String("body", "", "initial Markdown content")
	notes.AddCommand(create)
	notes.AddCommand(&cobra.Command{
		Use: "show <file>", Short: "Show a note", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			n, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = io.WriteString(streams.out, n.Body)
			return err
		},
	})
	edit := &cobra.Command{
		Use: "edit <file>", Short: "Replace a note's Markdown body", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("body") {
				return fmt.Errorf("nothing to change: pass --body")
			}
			body, _ := cmd.Flags().GetString("body")
			_, err = service.Update(cmd.Context(), args[0], body)
			return err
		},
	}
	edit.Flags().String("body", "", "new Markdown content")
	notes.AddCommand(edit)
	addDocumentCommands(root, streams)
	addSpreadsheetCommands(root, streams)
	addPresentationCommands(root, streams)

	root.AddCommand(tuiCommand())
	return root
}

func open(cmd *cobra.Command) (*filerepo.Repository, *note.Service, error) {
	repo, settings, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	return repo, note.NewService(repo, settings.UndoLimit), nil
}

func openDocuments(cmd *cobra.Command) (*filerepo.Repository, *document.Service, error) {
	repo, settings, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	return repo, document.NewService(repo, settings.UndoLimit), nil
}

func openSpreadsheets(cmd *cobra.Command) (*filerepo.Repository, *spreadsheet.Service, error) {
	repo, settings, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	return repo, spreadsheet.NewService(repo, settings.UndoLimit), nil
}

func openPresentations(cmd *cobra.Command) (*filerepo.Repository, *presentation.Service, error) {
	repo, settings, err := openStore()
	if err != nil {
		return nil, nil, err
	}
	return repo, presentation.NewService(repo, settings.UndoLimit), nil
}

// openStore resolves ~/.parchment/parchment.toml (or PARCHMENT_CONFIG) and
// returns a repository for standalone artifact files. The state directory
// holds only configuration, recovery drafts, and the write lock.
func openStore() (*filerepo.Repository, config.Settings, error) {
	configPath, err := config.Path()
	if err != nil {
		return nil, config.Settings{}, err
	}
	if os.Getenv("PARCHMENT_CONFIG") == "" {
		if err := config.EnsureDefault(configPath); err != nil {
			return nil, config.Settings{}, err
		}
	} else if _, err := os.Stat(configPath); err != nil {
		return nil, config.Settings{}, fmt.Errorf("PARCHMENT_CONFIG: %w", err)
	}
	settings, err := config.Load(configPath)
	if err != nil {
		return nil, config.Settings{}, err
	}
	stateDir, err := config.Directory()
	if err != nil {
		return nil, config.Settings{}, err
	}
	repo, err := filerepo.New(stateDir)
	if err != nil {
		return nil, config.Settings{}, err
	}
	return repo, settings, nil
}

// groupCommand makes a command group report unknown subcommands as errors
// instead of printing help and exiting successfully.
func groupCommand(group *cobra.Command) *cobra.Command {
	group.Args = cobra.NoArgs
	group.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	return group
}
