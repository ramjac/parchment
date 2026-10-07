package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
		Use:           "parchment",
		Short:         "Local-first notes, documents, spreadsheets, and presentations",
		Long:          "Each Parchment artifact is an ordinary Markdown file that you name and place anywhere.\nConfiguration is read from ~/.parchment/parchment.toml, which is created on first use.",
		Example:       "  parchment note create ~/Documents/ideas.md\n  parchment note show ~/Documents/ideas.md\n  parchment tui ~/Documents/ideas.md",
		SilenceUsage:  true,
		SilenceErrors: true,
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
			n, err := service.Create(cmd.Context(), path, titleFlag(cmd, path), body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", n.Path)
			return err
		},
	}
	create.Flags().String("title", "", "note title (defaults to the file name)")
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
			_, err = fmt.Fprintf(streams.out, "# %s\n\n%s", n.Title, n.Body)
			return err
		},
	})
	edit := &cobra.Command{
		Use: "edit <file>", Short: "Replace a note title or body", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			title, _ := cmd.Flags().GetString("title")
			body, _ := cmd.Flags().GetString("body")
			var titleValue, bodyValue *string
			if cmd.Flags().Changed("title") {
				titleValue = &title
			}
			if cmd.Flags().Changed("body") {
				bodyValue = &body
			}
			_, err = service.UpdateFields(cmd.Context(), args[0], titleValue, bodyValue)
			return err
		},
	}
	edit.Flags().String("title", "", "new note title")
	edit.Flags().String("body", "", "new Markdown content")
	notes.AddCommand(edit)
	notes.AddCommand(&cobra.Command{
		Use: "rename <file> <title>", Short: "Rename a note", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			_, err = service.Rename(cmd.Context(), args[0], args[1])
			return err
		},
	})
	notes.AddCommand(tagCommand("add"))
	notes.AddCommand(tagCommand("remove"))
	addDocumentCommands(root, streams)
	addSpreadsheetCommands(root, streams)
	addPresentationCommands(root, streams)

	root.AddCommand(tuiCommand())
	return root
}

func quoteFields(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return strings.Join(quoted, ", ")
}

func tagCommand(action string) *cobra.Command {
	title := strings.ToUpper(action[:1]) + action[1:]
	return &cobra.Command{
		Use: action + " <file> <tag>", Short: title + " a note tag", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			if action == "add" {
				return service.AddTag(cmd.Context(), args[0], args[1])
			}
			return service.RemoveTag(cmd.Context(), args[0], args[1])
		},
	}
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

// titleFlag returns --title, defaulting to the file's base name without its
// extension.
func titleFlag(cmd *cobra.Command, path string) string {
	if title, _ := cmd.Flags().GetString("title"); cmd.Flags().Changed("title") {
		return title
	}
	return defaultTitle(path)
}

func defaultTitle(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
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
