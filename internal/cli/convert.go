package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/mattn/go-isatty"

	"example.com/parchment/internal/artifact"
)

func newConvertCmd(streams output) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "convert <file>",
		Short: "Convert a plain Markdown file into a typed Parchment artifact in place",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			kindStr, _ := cmd.Flags().GetString("kind")
			kind := artifact.Kind(kindStr)
			if kind != artifact.DocumentKind && kind != artifact.NoteKind && kind != artifact.PresentationKind {
				return fmt.Errorf("unsupported artifact kind %q", kindStr)
			}

			repo, _, err := openStore()
			if err != nil {
				return err
			}

			absPath, err := filepath.Abs(path)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")
			isInteractive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())

			if !yes {
				if !isInteractive {
					return errors.New("conversion requires --yes when running non-interactively")
				}
				fmt.Fprintf(streams.out, "Convert %s to a Parchment %s in place? [y/N] ", path, kind)
				var response string
				fmt.Scanln(&response)
				response = strings.ToLower(strings.TrimSpace(response))
				if response != "y" && response != "yes" {
					return nil
				}
			}

			if err := repo.Convert(cmd.Context(), absPath, kind); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().String("kind", "document", "kind of artifact to convert to (document, note, presentation)")
	cmd.Flags().Bool("yes", false, "confirm conversion without prompting")
	return cmd
}
