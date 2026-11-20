package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/docinterop"
	"example.com/parchment/internal/document"
	"example.com/parchment/internal/note"
	"example.com/parchment/internal/pdfrender"
	"example.com/parchment/internal/presentation"
	"example.com/parchment/internal/printing"
	"example.com/parchment/internal/spreadsheet"
)

func addDocumentCommands(root *cobra.Command, streams output) {
	docs := &cobra.Command{
		Use: "document", Aliases: []string{"doc"}, Short: "Create and manage multi-page documents",
		Long: "Documents are Markdown with page layout: multiple pages, columns, section and page\n" +
			"breaks, embedded images, headers, footers, page numbers, and margins. Use\n" +
			"`document print` for printer-ready text.",
	}
	root.AddCommand(groupCommand(docs))

	docs.AddCommand(
		newDocumentCreateCmd(streams),
		newDocumentShowCmd(streams),
		newDocumentEditCmd(),
		newDocumentTrackingCmd(streams),
		newDocumentChangesCmd(streams),
		newDocumentReviewCmd(streams),
		newDocumentAcceptCmd(),
		newDocumentRejectCmd(),
		newDocumentLayoutCmd(streams),
		newDocumentPageBreakCmd(),
		newDocumentSectionBreakCmd(),
		newDocumentImageCmd(streams),
		newDocumentImagesCmd(streams),
		newDocumentImageExtractCmd(streams),
		newDocumentImageRemoveCmd(),
		newDocumentPrintCmd(streams),
		newDocumentExportCmd(streams),
		newDocumentImportCmd(streams),
	)
}

func newDocumentCreateCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "create <file>", Short: "Create a document file", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
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
			path, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			d, err := service.Create(cmd.Context(), path, document.Draft{Body: body, Layout: layout})
			if err != nil {
				return err
			}
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			return finishCreate(cmd, streams, d.Path, artifact.DocumentKind)
		},
	}
	addBodyFlags(cmd)
	addLayoutFlags(cmd)
	addEditFlag(cmd)
	return cmd
}

func newDocumentShowCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "show <file>", Short: "Show a document's Markdown", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			warnDocumentTracking(streams.err, d)
			_, err = io.WriteString(streams.out, d.Body)
			return err
		},
	}
}

func newDocumentEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "edit <file>", Short: "Replace a document's Markdown body", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			body, bodyChanged, err := bodyFromFlags(cmd)
			if err != nil {
				return err
			}
			if !bodyChanged {
				return fmt.Errorf("nothing to change: pass --body or --body-file")
			}
			track, err := documentTrackingChoice(cmd)
			if err != nil {
				return err
			}
			_, err = service.ModifyWithTrackingChoice(cmd.Context(), args[0], "Edit document", track, func(d *document.Document) error {
				d.Body = body
				return nil
			})
			if err == nil {
				drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			}
			return err
		},
	}
	addBodyFlags(cmd)
	cmd.Flags().Bool("track", false, "record this edit for review")
	return cmd
}

func newDocumentTrackingCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "tracking <file> [on|off]", Short: "Show or change document tracking", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(args) == 2 {
				if args[1] != "on" && args[1] != "off" {
					return errors.New("tracking must be on or off")
				}
				d, err = service.SetTracking(cmd.Context(), args[0], args[1] == "on")
				if err != nil {
					return err
				}
			}
			state := "off"
			if d.Tracking {
				state = "on"
			}
			_, err = fmt.Fprintln(streams.out, state)
			return err
		},
	}
}

func newDocumentChangesCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "changes <file>", Short: "List pending tracked edits", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			for _, change := range d.TrackedChanges {
				if _, err := fmt.Fprintf(streams.out, "%s\tpending\t%s\tbyte %d\n",
					change.ID, change.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), change.Offset); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newDocumentReviewCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "review <file> <change-id>", Short: "Review a tracked document edit", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, change := range d.TrackedChanges {
				if change.ID == args[1] {
					if _, err = fmt.Fprintf(streams.out, "change: %s\nstatus: pending\n\n%s\n", change.ID, document.TrackingSummary(change)); err != nil {
						return err
					}
					if change.Group != "" {
						if _, err = fmt.Fprintln(streams.out, "\nLinked image move (reviewed together):"); err != nil {
							return err
						}
						for _, linked := range d.TrackedChanges {
							if linked.ID != change.ID && linked.Group == change.Group {
								if _, err = fmt.Fprintf(streams.out, "\n%s\n", document.TrackingSummary(linked)); err != nil {
									return err
								}
							}
						}
					}
					return nil
				}
			}
			return document.ErrChangeNotFound
		},
	}
}

func newDocumentAcceptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "accept <file> <change-id|--all>", Short: "Accept tracked edits", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			id, err := documentReviewID(cmd, args)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err == nil {
				_, err = service.ReviewTracked(cmd.Context(), d, id, true)
			}
			if err == nil {
				drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			}
			return err
		},
	}
	cmd.Flags().Bool("all", false, "accept all pending changes")
	return cmd
}

func newDocumentRejectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "reject <file> <change-id|--all>", Short: "Reject tracked edits", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			id, err := documentReviewID(cmd, args)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err == nil {
				_, err = service.ReviewTracked(cmd.Context(), d, id, false)
			}
			return err
		},
	}
	cmd.Flags().Bool("all", false, "reject all pending changes")
	return cmd
}

func documentReviewID(cmd *cobra.Command, args []string) (string, error) {
	all, _ := cmd.Flags().GetBool("all")
	if all == (len(args) == 2) {
		return "", errors.New("supply exactly one change ID or --all")
	}
	if all {
		return "", nil
	}
	return args[1], nil
}

func warnDocumentTracking(output io.Writer, d document.Document) {
	if len(d.TrackedChanges) > 0 {
		fmt.Fprintf(output, "warning: %d pending tracked change(s) not exported\n", len(d.TrackedChanges))
	}
}

func documentTrackingChoice(cmd *cobra.Command) (*bool, error) {
	if !cmd.Flags().Changed("track") {
		return nil, nil
	}
	track, err := cmd.Flags().GetBool("track")
	return &track, err
}

func newDocumentLayoutCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "layout <file>", Short: "Show or change page layout",
		Long: "Show a document's page layout, or change the layout fields given as flags.\n" +
			"Margins are in millimeters. Header and footer text may contain {title}, {page},\n" +
			"and {pages}, and \"|\" separates left, center, and right parts.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
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
				drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			}
			l := d.Layout
			_, err = fmt.Fprintf(streams.out, "page-size: %s\norientation: %s\nmargins: top=%d right=%d bottom=%d left=%d (mm)\ncolumns: %d\nheader: %q\nfooter: %q\npage-numbers: %s\nfont: %s\n",
				l.PageSize, l.Orientation, l.Margins.Top, l.Margins.Right, l.Margins.Bottom, l.Margins.Left,
				l.Columns, l.Header, l.Footer, l.PageNumbers, l.Font)
			return err
		},
	}
	addLayoutFlags(cmd)
	return cmd
}

func newDocumentPageBreakCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "page-break <file>", Short: "Append a page break", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return appendMarkup(cmd, args[0], "Insert page break", document.PageBreakMarkup)
		},
	}
	cmd.Flags().Bool("track", false, "record this edit for review")
	return cmd
}

func newDocumentSectionBreakCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "section-break <file>", Short: "Append a section break with a column count", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			columns, _ := cmd.Flags().GetInt("columns")
			if columns < 1 || columns > document.MaxColumns {
				return fmt.Errorf("columns must be between 1 and %d", document.MaxColumns)
			}
			continuous, _ := cmd.Flags().GetBool("continuous")
			return appendMarkup(cmd, args[0], "Insert section break", document.SectionBreakMarkup(columns, continuous))
		},
	}
	cmd.Flags().Int("columns", 1, "columns in the new section")
	cmd.Flags().Bool("continuous", false, "start the section on the current page")
	cmd.Flags().Bool("track", false, "record this edit for review")
	return cmd
}

func newDocumentImageCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "image <file> <image-file>", Short: "Embed a PNG, JPEG, or GIF image at the end of a document", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			alt, _ := cmd.Flags().GetString("alt")
			track, err := documentTrackingChoice(cmd)
			if err != nil {
				return err
			}
			_, name, err := service.AddImageWithTrackingChoice(cmd.Context(), args[0], alt, data, track)
			if err != nil {
				return err
			}
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			_, err = fmt.Fprintf(streams.out, "%s\n", name)
			return err
		},
	}
	cmd.Flags().String("alt", "", "alternative text for the image")
	cmd.Flags().Bool("track", false, "record this edit for review")
	return cmd
}

func newDocumentImagesCmd(streams output) *cobra.Command {
	return &cobra.Command{
		Use: "images <file>", Short: "List embedded images in a document", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			referenced := artifact.ReferencedImages(d.Body)
			for _, img := range d.Images {
				status := "unreferenced"
				if referenced[img.Name] {
					status = "referenced"
				}
				format := "unknown"
				width, height := 0, 0
				if config, f, err := image.DecodeConfig(bytes.NewReader(img.Data)); err == nil {
					format = f
					width = config.Width
					height = config.Height
				}
				if _, err := fmt.Fprintf(streams.out, "%s\t%s\t%dx%d\t%d\t%s\n", img.Name, format, width, height, len(img.Data), status); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newDocumentImageExtractCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "image-extract <file> <image-name>", Short: "Extract an embedded image from a document", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			d, err := service.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			var imgData []byte
			for _, img := range d.Images {
				if img.Name == args[1] {
					imgData = img.Data
					break
				}
			}
			if imgData == nil {
				return fmt.Errorf("image %q not found in document", args[1])
			}

			outPath, _ := cmd.Flags().GetString("output")
			if outPath == "" {
				_, err = streams.out.Write(imgData)
				return err
			}

			force, _ := cmd.Flags().GetBool("force")
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if !force {
				flags |= os.O_EXCL
			}

			f, err := os.OpenFile(outPath, flags, 0600)
			if err != nil {
				if os.IsExist(err) {
					return fmt.Errorf("file %q already exists (use --force to overwrite)", outPath)
				}
				return err
			}
			if _, err := f.Write(imgData); err != nil {
				f.Close()
				return err
			}
			return f.Close()
		},
	}
	cmd.Flags().StringP("output", "o", "", "write to a file instead of standard output")
	cmd.Flags().Bool("force", false, "overwrite output file if it exists")
	return cmd
}

func newDocumentImageRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "image-remove <file> <image-name>", Short: "Remove an embedded image from a document", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}
			strip, _ := cmd.Flags().GetBool("strip-references")
			track, err := documentTrackingChoice(cmd)
			if err != nil {
				return err
			}
			_, err = service.RemoveImageWithTrackingChoice(cmd.Context(), args[0], args[1], strip, track)
			return err
		},
	}
	cmd.Flags().Bool("strip-references", false, "remove Markdown image tags referencing the image from the document body")
	cmd.Flags().Bool("track", false, "record this edit for review")
	return cmd
}

func newDocumentPrintCmd(streams output) *cobra.Command {
	return newPrintCmd(artifact.DocumentKind, streams)
}

func newPrintCmd(kind artifact.Kind, streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use: "print <file>", Short: "Render printer-ready text or PDF", Args: cobra.ExactArgs(1),
		Long: fmt.Sprintf("Render a %s as paginated text or PDF. Text pages are separated by form feeds\n"+
			"and can be sent directly to a printer, for example `parchment %s print <file> | lpr`.", kind, kind),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, settings, err := openStore()
			if err != nil {
				return err
			}
			var d document.Document
			var deck presentation.Presentation
			var book spreadsheet.Spreadsheet
			switch kind {
			case artifact.NoteKind:
				n, getErr := note.NewService(repo, settings.UndoLimit).Get(cmd.Context(), args[0])
				if getErr != nil {
					return getErr
				}
				d = printing.NoteDocument(n)
			case artifact.DocumentKind:
				d, err = document.NewService(repo, settings.UndoLimit).Get(cmd.Context(), args[0])
				if err != nil {
					return err
				}
			case artifact.PresentationKind:
				deck, err = presentation.NewService(repo, settings.UndoLimit).Get(cmd.Context(), args[0])
				if err != nil {
					return err
				}
			case artifact.SpreadsheetKind:
				book, err = spreadsheet.NewService(repo, settings.UndoLimit).Get(cmd.Context(), args[0])
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported print kind %q", kind)
			}
			if kind == artifact.DocumentKind {
				warnDocumentTracking(streams.err, d)
			}
			format, _ := cmd.Flags().GetString("format")
			slidesPerPage := 1
			if kind == artifact.PresentationKind {
				slidesPerPage, _ = cmd.Flags().GetInt("slides-per-page")
				if slidesPerPage != 1 && slidesPerPage != 2 {
					return fmt.Errorf("slides-per-page must be 1 or 2")
				}
			}
			var data []byte
			switch format {
			case "text":
				var text string
				if kind == artifact.SpreadsheetKind {
					text, err = printing.PrintSpreadsheet(book)
				} else if kind == artifact.PresentationKind && slidesPerPage == 2 {
					text, err = printing.PrintPresentationTwoUp(deck)
				} else if kind == artifact.PresentationKind {
					text, err = printing.PrintPresentation(deck)
				} else {
					text, err = document.Print(d)
				}
				if err != nil {
					return err
				}
				data = []byte(text)
			case "pdf":
				var rendered bytes.Buffer
				if kind == artifact.SpreadsheetKind {
					err = pdfrender.RenderSpreadsheet(&rendered, book)
				} else if kind == artifact.PresentationKind && slidesPerPage == 2 {
					err = pdfrender.RenderPresentationTwoUp(&rendered, deck)
				} else if kind == artifact.PresentationKind {
					err = pdfrender.RenderPresentation(&rendered, deck)
				} else {
					err = pdfrender.Render(&rendered, d)
				}
				if err != nil {
					return err
				}
				data = rendered.Bytes()
			default:
				return fmt.Errorf("unsupported print format %q (use text or pdf)", format)
			}
			path, _ := cmd.Flags().GetString("output")
			if path != "" {
				if format == "text" {
					err := repo.WriteTextExport(cmd.Context(), path, args[0], data, false)
					if errors.Is(err, fs.ErrExist) {
						force, _ := cmd.Flags().GetBool("force")
						if !force {
							if !streamsInteractive(cmd.InOrStdin(), cmd.ErrOrStderr()) {
								return fmt.Errorf("file %q already exists (use --force to overwrite)", path)
							}
							confirmed, promptErr := confirmOutputOverwrite(cmd.InOrStdin(), cmd.ErrOrStderr(), path)
							if promptErr != nil {
								return promptErr
							}
							if !confirmed {
								return nil
							}
						}
						err = repo.WriteTextExport(cmd.Context(), path, args[0], data, true)
					}
					return err
				}
				err := repo.WritePDFExport(cmd.Context(), path, args[0], data, false)
				if errors.Is(err, fs.ErrExist) {
					force, _ := cmd.Flags().GetBool("force")
					if !force {
						if !streamsInteractive(cmd.InOrStdin(), cmd.ErrOrStderr()) {
							return fmt.Errorf("file %q already exists (use --force to overwrite)", path)
						}
						confirmed, promptErr := confirmPDFOverwrite(cmd.InOrStdin(), cmd.ErrOrStderr(), path)
						if promptErr != nil {
							return promptErr
						}
						if !confirmed {
							return nil
						}
					}
					err = repo.WritePDFExport(cmd.Context(), path, args[0], data, true)
				}
				if err != nil {
					return err
				}
				if err := repo.SavePDFPath(cmd.Context(), path); err != nil {
					fmt.Fprintf(streams.err, "warning: could not remember PDF path: %v\n", err)
				}
				return nil
			}
			_, err = streams.out.Write(data)
			return err
		},
	}
	cmd.Flags().String("format", "text", "output format: text or pdf")
	cmd.Flags().StringP("output", "o", "", "write to a file instead of standard output")
	cmd.Flags().Bool("force", false, "overwrite output file if it exists")
	if kind == artifact.PresentationKind {
		cmd.Flags().Int("slides-per-page", 1, "slides per page: 1 (landscape) or 2 (portrait)")
	}
	return cmd
}

func confirmPDFOverwrite(in io.Reader, out io.Writer, path string) (bool, error) {
	if _, err := fmt.Fprintf(out, "PDF file %q already exists. Overwrite? [y/N] ", path); err != nil {
		return false, err
	}
	return readOverwriteConfirmation(in)
}

func confirmOutputOverwrite(in io.Reader, out io.Writer, path string) (bool, error) {
	if _, err := fmt.Fprintf(out, "File %q already exists. Overwrite? [y/N] ", path); err != nil {
		return false, err
	}
	return readOverwriteConfirmation(in)
}

func readOverwriteConfirmation(in io.Reader) (bool, error) {
	response, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
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
	flags.String("font", "", "font family: courier, helvetica, or times")
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
	setString("font", &layout.Font)
	layout.Font = strings.ToLower(layout.Font)
	return layout.Validate()
}

func appendMarkup(cmd *cobra.Command, id, description, markup string) error {
	repo, service, err := openDocuments(cmd)
	if err != nil {
		return err
	}
	track, err := documentTrackingChoice(cmd)
	if err != nil {
		return err
	}
	_, err = service.ModifyWithTrackingChoice(cmd.Context(), id, description, track, func(d *document.Document) error {
		d.Body = trimTrailingNewlines(d.Body) + "\n\n" + markup + "\n\n"
		return nil
	})
	if err == nil {
		drainWarnings(repo, output{err: cmd.ErrOrStderr()})
	}
	return err
}

func trimTrailingNewlines(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func newDocumentExportCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <file> -o <target.odt|target.docx>",
		Short: "Export a document to DOCX or ODF format",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputPath, _ := cmd.Flags().GetString("output")
			if outputPath == "" {
				return errors.New("output file is required (-o/--output)")
			}

			format, _ := cmd.Flags().GetString("format")
			if format == "" {
				ext := strings.ToLower(filepath.Ext(outputPath))
				switch ext {
				case ".docx":
					format = "docx"
				case ".odt":
					format = "odt"
				default:
					return fmt.Errorf("unable to determine export format from %q (use --format)", outputPath)
				}
			} else {
				format = strings.ToLower(format)
			}

			var exporter docinterop.Exporter
			switch format {
			case "docx":
				exporter = docinterop.NewDocxExporter()
			case "odt":
				exporter = docinterop.NewOdtExporter()
			default:
				return fmt.Errorf("unsupported export format %q", format)
			}

			force, _ := cmd.Flags().GetBool("force")
			mode := os.FileMode(0o600)
			if info, err := os.Stat(outputPath); err == nil {
				mode = info.Mode().Perm()
				if !force {
					return fmt.Errorf("%s already exists (use --force to overwrite)", outputPath)
				}
			}

			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}

			srcPath, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}

			doc, err := service.Get(cmd.Context(), srcPath)
			if err != nil {
				return err
			}

			warnDocumentTracking(streams.err, doc)

			var buf bytes.Buffer
			warnings, err := exporter.Export(&buf, doc)
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintf(streams.err, "%s\n", w.Error())
			}

			// Write atomically beside target
			dir := filepath.Dir(outputPath)
			tempFile, err := os.CreateTemp(dir, ".parchment-export-*")
			if err != nil {
				return err
			}
			tempName := tempFile.Name()
			written := false
			defer func() {
				if !written {
					_ = os.Remove(tempName)
				}
			}()

			if err := tempFile.Chmod(mode); err != nil {
				_ = tempFile.Close()
				return err
			}
			if _, err := tempFile.Write(buf.Bytes()); err != nil {
				_ = tempFile.Close()
				return err
			}
			if err := tempFile.Sync(); err != nil {
				_ = tempFile.Close()
				return err
			}
			if err := tempFile.Close(); err != nil {
				return err
			}
			if err := os.Rename(tempName, outputPath); err != nil {
				return err
			}
			written = true
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			return nil
		},
	}
	cmd.Flags().StringP("output", "o", "", "output file (required)")
	cmd.Flags().String("format", "", "export format: docx or odt")
	cmd.Flags().Bool("force", false, "overwrite output file if it exists")
	return cmd
}

func newDocumentImportCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <source> <target.md>",
		Short: "Import a document from DOCX or ODF format",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcPath, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			targetPath, err := filepath.Abs(args[1])
			if err != nil {
				return err
			}

			// Refuse to overwrite target if it already exists
			if _, err := os.Stat(targetPath); err == nil {
				return fmt.Errorf("%s already exists", targetPath)
			}

			format, _ := cmd.Flags().GetString("format")
			if format == "" {
				ext := strings.ToLower(filepath.Ext(srcPath))
				switch ext {
				case ".docx":
					format = "docx"
				case ".odt":
					format = "odt"
				default:
					return fmt.Errorf("unable to determine import format from %q (use --format)", srcPath)
				}
			} else {
				format = strings.ToLower(format)
			}

			var importer docinterop.Importer
			switch format {
			case "docx":
				importer = docinterop.NewDocxImporter()
			case "odt":
				importer = docinterop.NewOdtImporter()
			default:
				return fmt.Errorf("unsupported import format %q", format)
			}

			srcFile, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer srcFile.Close()

			fi, err := srcFile.Stat()
			if err != nil {
				return err
			}

			draft, warnings, err := importer.Import(srcFile, fi.Size())
			if err != nil {
				return err
			}
			for _, w := range warnings {
				fmt.Fprintf(streams.err, "%s\n", w.Error())
			}

			repo, service, err := openDocuments(cmd)
			if err != nil {
				return err
			}

			d, err := service.Create(cmd.Context(), targetPath, draft)
			if err != nil {
				return err
			}
			drainWarnings(repo, output{err: cmd.ErrOrStderr()})
			return finishCreate(cmd, streams, d.Path, artifact.DocumentKind)
		},
	}
	cmd.Flags().String("format", "", "import format: docx or odt")
	addEditFlag(cmd)
	return cmd
}
