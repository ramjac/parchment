package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/document"
	"example.com/parchment/internal/search"
)

func addDocumentCommands(root *cobra.Command, streams output) {
	docs := &cobra.Command{
		Use: "document", Aliases: []string{"doc"}, Short: "Create and manage multi-page documents",
		Long: "Documents are Markdown with page layout: multiple pages, columns, section and page\n" +
			"breaks, embedded images, headers, footers, page numbers, and margins. Use\n" +
			"`document print` for printer-ready text.",
	}
	root.AddCommand(docs)

	docs.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List documents", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			documents, err := service.List(cmd.Context())
			if err != nil {
				return err
			}
			for _, d := range documents {
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\t%s\n", d.ID, strconv.Quote(d.Title), quoteFields(d.Tags)); err != nil {
					return err
				}
			}
			return nil
		},
	})

	create := &cobra.Command{
		Use: "create <title>", Short: "Create a document", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			body, _, err := bodyFromFlags(cmd)
			if err != nil {
				return err
			}
			layout := document.DefaultLayout()
			if err := applyLayoutFlags(cmd, &layout); err != nil {
				return err
			}
			d, err := service.Create(cmd.Context(), document.Draft{Title: args[0], Body: body, Layout: layout})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", d.ID)
			return err
		},
	}
	addBodyFlags(create)
	addLayoutFlags(create)
	docs.AddCommand(create)

	docs.AddCommand(&cobra.Command{
		Use: "show <id>", Short: "Show a document's Markdown", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "# %s\n\n%s", d.Title, d.Body)
			return err
		},
	})

	edit := &cobra.Command{
		Use: "edit <id>", Short: "Replace a document title or body", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			body, bodyChanged, err := bodyFromFlags(cmd)
			if err != nil {
				return err
			}
			title, _ := cmd.Flags().GetString("title")
			titleChanged := cmd.Flags().Changed("title")
			_, err = service.Modify(cmd.Context(), args[0], "Edit document", func(d *document.Document) error {
				if titleChanged {
					d.Title = title
				}
				if bodyChanged {
					d.Body = body
				}
				return nil
			})
			return err
		},
	}
	edit.Flags().String("title", "", "new document title")
	addBodyFlags(edit)
	docs.AddCommand(edit)

	docs.AddCommand(&cobra.Command{
		Use: "rename <id> <title>", Short: "Rename a document", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			_, err = service.Rename(cmd.Context(), args[0], args[1])
			return err
		},
	})

	for _, action := range []string{"add", "remove"} {
		action := action
		docs.AddCommand(&cobra.Command{
			Use: "tag-" + action + " <id> <tag>", Short: map[string]string{"add": "Add", "remove": "Remove"}[action] + " a document tag",
			Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				_, service, err := openDocuments(cmd)
				if err != nil {
					return err
				}
				if action == "add" {
					return service.AddTag(cmd.Context(), args[0], args[1])
				}
				return service.RemoveTag(cmd.Context(), args[0], args[1])
			},
		})
	}

	layoutCommand := &cobra.Command{
		Use: "layout <id>", Short: "Show or change page layout",
		Long: "Show a document's page layout, or change the layout fields given as flags.\n" +
			"Margins are in millimeters. Header and footer text may contain {title}, {page},\n" +
			"and {pages}, and \"|\" separates left, center, and right parts.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			layout := d.Layout
			if err := applyLayoutFlags(cmd, &layout); err != nil {
				return err
			}
			if layout != d.Layout {
				if d, err = service.SetLayout(cmd.Context(), args[0], layout); err != nil {
					return err
				}
			}
			l := d.Layout
			_, err = fmt.Fprintf(streams.out, "page-size: %s\norientation: %s\nmargins: top=%d right=%d bottom=%d left=%d (mm)\ncolumns: %d\nheader: %q\nfooter: %q\npage-numbers: %s\n",
				l.PageSize, l.Orientation, l.Margins.Top, l.Margins.Right, l.Margins.Bottom, l.Margins.Left,
				l.Columns, l.Header, l.Footer, l.PageNumbers)
			return err
		},
	}
	addLayoutFlags(layoutCommand)
	docs.AddCommand(layoutCommand)

	docs.AddCommand(&cobra.Command{
		Use: "page-break <id>", Short: "Append a page break", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return appendMarkup(cmd, args[0], "Insert page break", document.PageBreakMarkup)
		},
	})
	sectionBreak := &cobra.Command{
		Use: "section-break <id>", Short: "Append a section break with a column count", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			columns, _ := cmd.Flags().GetInt("columns")
			if columns < 1 || columns > document.MaxColumns {
				return fmt.Errorf("columns must be between 1 and %d", document.MaxColumns)
			}
			continuous, _ := cmd.Flags().GetBool("continuous")
			return appendMarkup(cmd, args[0], "Insert section break", document.SectionBreakMarkup(columns, continuous))
		},
	}
	sectionBreak.Flags().Int("columns", 1, "columns in the new section")
	sectionBreak.Flags().Bool("continuous", false, "start the section on the current page")
	docs.AddCommand(sectionBreak)

	image := &cobra.Command{
		Use: "image <id> <file>", Short: "Embed a PNG, JPEG, or GIF image at the end of a document", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			alt, _ := cmd.Flags().GetString("alt")
			_, name, err := service.AddImage(cmd.Context(), args[0], alt, data)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.out, "%s\n", name)
			return err
		},
	}
	image.Flags().String("alt", "", "alternative text for the image")
	docs.AddCommand(image)

	printCommand := &cobra.Command{
		Use: "print <id>", Short: "Render printer-ready paginated text", Args: cobra.ExactArgs(1),
		Long: "Render the document as monospaced pages with margins, columns, headers, footers,\n" +
			"and page numbers. Pages are separated by form feeds, so the output can be sent\n" +
			"directly to a printer, for example `parchment document print <id> | lpr`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			text, err := document.Print(d)
			if err != nil {
				return err
			}
			if path, _ := cmd.Flags().GetString("output"); path != "" {
				return os.WriteFile(path, []byte(text), 0o644)
			}
			_, err = io.WriteString(streams.out, text)
			return err
		},
	}
	printCommand.Flags().StringP("output", "o", "", "write to a file instead of standard output")
	docs.AddCommand(printCommand)

	deleteCommand := &cobra.Command{
		Use: "delete <id>", Short: "Permanently delete a document", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if yes, _ := cmd.Flags().GetBool("yes"); !yes {
				return errors.New("deletion requires --yes")
			}
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			return service.Delete(cmd.Context(), args[0])
		},
	}
	deleteCommand.Flags().Bool("yes", false, "confirm permanent deletion")
	docs.AddCommand(deleteCommand)

	docs.AddCommand(&cobra.Command{
		Use: "search <query>", Short: "Search document titles, Markdown, and tags", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, _, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			results, err := search.Documents(cmd.Context(), ws, args[0])
			if err != nil {
				return err
			}
			for _, d := range results {
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\n", d.ID, strconv.Quote(d.Title)); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func addBodyFlags(cmd *cobra.Command) {
	cmd.Flags().String("body", "", "Markdown content")
	cmd.Flags().String("body-file", "", "read Markdown content from a file (- for standard input)")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
}

// bodyFromFlags returns the body supplied by --body or --body-file, and
// whether either flag was used.
func bodyFromFlags(cmd *cobra.Command) (string, bool, error) {
	if cmd.Flags().Changed("body") {
		body, _ := cmd.Flags().GetString("body")
		return body, true, nil
	}
	path, _ := cmd.Flags().GetString("body-file")
	if path == "" {
		return "", false, nil
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(cmd.InOrStdin())
	} else {
		data, err = os.ReadFile(path)
	}
	return string(data), true, err
}

func addLayoutFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.String("page-size", "", "page size: letter, a4, or legal")
	flags.String("orientation", "", "portrait or landscape")
	flags.Int("margin", 0, "all four margins, in millimeters")
	flags.Int("margin-top", 0, "top margin in millimeters")
	flags.Int("margin-right", 0, "right margin in millimeters")
	flags.Int("margin-bottom", 0, "bottom margin in millimeters")
	flags.Int("margin-left", 0, "left margin in millimeters")
	flags.Int("columns", 0, "default number of text columns (1-4)")
	flags.String("header", "", "header text")
	flags.String("footer", "", "footer text")
	flags.String("page-numbers", "", "page number placement: none, left, center, or right")
}

// applyLayoutFlags changes only the layout fields whose flags were supplied.
func applyLayoutFlags(cmd *cobra.Command, layout *document.Layout) error {
	flags := cmd.Flags()
	setString := func(name string, target *string) {
		if flags.Changed(name) {
			*target, _ = flags.GetString(name)
		}
	}
	setInt := func(name string, targets ...*int) {
		if flags.Changed(name) {
			value, _ := flags.GetInt(name)
			for _, target := range targets {
				*target = value
			}
		}
	}
	setString("page-size", &layout.PageSize)
	setString("orientation", &layout.Orientation)
	setInt("margin", &layout.Margins.Top, &layout.Margins.Right, &layout.Margins.Bottom, &layout.Margins.Left)
	setInt("margin-top", &layout.Margins.Top)
	setInt("margin-right", &layout.Margins.Right)
	setInt("margin-bottom", &layout.Margins.Bottom)
	setInt("margin-left", &layout.Margins.Left)
	setInt("columns", &layout.Columns)
	setString("header", &layout.Header)
	setString("footer", &layout.Footer)
	setString("page-numbers", &layout.PageNumbers)
	return layout.Validate()
}

func appendMarkup(cmd *cobra.Command, id, description, markup string) error {
	_, service, err := openDocuments(cmd)
	if err != nil {
		return err
	}
	_, err = service.Modify(cmd.Context(), id, description, func(d *document.Document) error {
		d.Body = trimTrailingNewlines(d.Body) + "\n\n" + markup + "\n\n"
		return nil
	})
	return err
}

func trimTrailingNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
