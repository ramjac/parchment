package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/config"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/search"
	"example.com/parchment/internal/spreadsheet"
	"example.com/parchment/internal/tui"
	"example.com/parchment/internal/workspace"
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
		Short:         "A local-first productivity workspace",
		Example:       "  parchment init ~/Documents/work\n  parchment --workspace ~/Documents/work note create \"First note\"\n  parchment search project",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().String("workspace", "", "workspace directory")

	root.AddCommand(&cobra.Command{
		Use: "init [directory]", Short: "Create a local workspace", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := os.Getwd()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				path = args[0]
			}
			if value, _ := cmd.InheritedFlags().GetString("workspace"); value != "" {
				path = value
			}
			if err := workspace.Init(path); err != nil {
				return err
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "Initialized workspace at %s\n", abs)
			return err
		},
	})

	notes := &cobra.Command{Use: "note", Short: "Create and manage Markdown notes"}
	root.AddCommand(notes)
	notes.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List notes", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			notes, err := service.List(cmd.Context())
			if err != nil {
				return err
			}
			for _, n := range notes {
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\t%s\n", n.ID, strconv.Quote(n.Title), quoteFields(n.Tags)); err != nil {
					return err
				}
			}
			return nil
		},
	})
	create := &cobra.Command{
		Use: "create <title>", Short: "Create a note", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			body, _ := cmd.Flags().GetString("body")
			n, err := service.Create(cmd.Context(), args[0], body)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", n.ID)
			return err
		},
	}
	create.Flags().String("body", "", "initial Markdown content")
	notes.AddCommand(create)
	notes.AddCommand(&cobra.Command{
		Use: "show <id>", Short: "Show a note", Args: cobra.ExactArgs(1),
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
		Use: "edit <id>", Short: "Replace a note title or body", Args: cobra.ExactArgs(1),
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
		Use: "rename <id> <title>", Short: "Rename a note", Args: cobra.ExactArgs(2),
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
	deleteNote := &cobra.Command{
		Use: "delete <id>", Short: "Permanently delete a note", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				return errors.New("deletion requires --yes")
			}
			_, service, err := open(cmd)
			if err != nil {
				return err
			}
			return service.Delete(cmd.Context(), args[0])
		},
	}
	deleteNote.Flags().Bool("yes", false, "confirm permanent deletion")
	notes.AddCommand(deleteNote)

	addDocumentCommands(root, streams)
	addSpreadsheetCommands(root, streams)
	addPresentationCommands(root, streams)

	root.AddCommand(&cobra.Command{
		Use: "search <query>", Short: "Search note titles, Markdown, and tags", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, _, err := open(cmd)
			if err != nil {
				return err
			}
			results, err := search.Notes(cmd.Context(), ws, args[0])
			if err != nil {
				return err
			}
			for _, n := range results {
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\n", n.ID, strconv.Quote(n.Title)); err != nil {
					return err
				}
			}
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "tui", Short: "Open the interactive notes and documents workspace", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws, settings, err := openWorkspace(cmd)
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), note.NewService(ws, settings.UndoLimit), ws, ws.Name(), ws.Root(),
				tui.WithDocuments(document.NewService(ws, settings.UndoLimit)))
		},
	})
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
		Use: action + " <id> <tag>", Short: title + " a note tag", Args: cobra.ExactArgs(2),
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

func open(cmd *cobra.Command) (*workspace.Workspace, *note.Service, error) {
	ws, settings, err := openWorkspace(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ws, note.NewService(ws, settings.UndoLimit), nil
}

func openDocuments(cmd *cobra.Command) (*workspace.Workspace, *document.Service, error) {
	ws, settings, err := openWorkspace(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ws, document.NewService(ws, settings.UndoLimit), nil
}

func openSpreadsheets(cmd *cobra.Command) (*workspace.Workspace, *spreadsheet.Service, error) {
	ws, settings, err := openWorkspace(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ws, spreadsheet.NewService(ws, settings.UndoLimit), nil
}

func openPresentations(cmd *cobra.Command) (*workspace.Workspace, *presentation.Service, error) {
	ws, settings, err := openWorkspace(cmd)
	if err != nil {
		return nil, nil, err
	}
	return ws, presentation.NewService(ws, settings.UndoLimit), nil
}

func openWorkspace(cmd *cobra.Command) (*workspace.Workspace, config.Settings, error) {
	userPath, err := config.UserConfigPath()
	if err != nil {
		return nil, config.Settings{}, err
	}
	userSettings, err := config.Load(userPath, "")
	if err != nil {
		return nil, config.Settings{}, err
	}
	path, _ := cmd.InheritedFlags().GetString("workspace")
	if path == "" {
		path = os.Getenv("PARCHMENT_WORKSPACE")
	}
	if path == "" {
		path = userSettings.WorkspacePath
	}
	if path == "" && userSettings.WorkspaceDiscovery == "parents" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, config.Settings{}, err
		}
		path, err = workspace.Find(cwd)
		if err != nil {
			return nil, config.Settings{}, err
		}
	}
	if path == "" {
		return nil, config.Settings{}, errors.New("workspace path is required; pass --workspace or set PARCHMENT_WORKSPACE")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, config.Settings{}, err
	}
	if err := workspace.ValidateMarker(abs); err != nil {
		return nil, config.Settings{}, err
	}
	settings, err := config.Load(userPath, filepath.Join(abs, "parchment.toml"))
	if err != nil {
		return nil, config.Settings{}, err
	}
	ws, err := workspace.Open(abs)
	if err != nil {
		return nil, config.Settings{}, err
	}
	return ws, settings, nil
}
