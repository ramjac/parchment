package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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

			absPath, err := filepath.Abs(path)
			if err != nil {
				return err
			}

			yes, _ := cmd.Flags().GetBool("yes")
			if !yes {
				if !streamsInteractive(cmd.InOrStdin(), cmd.OutOrStdout()) {
					return errors.New("conversion requires --yes when running non-interactively")
				}
				confirmed, err := confirmConversion(cmd.InOrStdin(), cmd.OutOrStdout(), path, kind)
				if err != nil {
					return err
				}
				if !confirmed {
					return nil
				}
			}

			repo, _, err := openStore()
			if err != nil {
				return err
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

func confirmConversion(in io.Reader, out io.Writer, path string, kind artifact.Kind) (bool, error) {
	if _, err := fmt.Fprintf(out, "Convert %s to a Parchment %s in place? [y/N] ", path, kind); err != nil {
		return false, fmt.Errorf("write conversion prompt: %w", err)
	}
	response, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read conversion confirmation: %w", err)
	}
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}
