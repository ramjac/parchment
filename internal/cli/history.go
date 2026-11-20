package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newHistoryCmd() *cobra.Command {
	group := groupCommand(&cobra.Command{Use: "history", Short: "Inspect and manage saved-file history"})
	group.AddCommand(
		newHistoryTransitionCmd("undo"),
		newHistoryTransitionCmd("redo"),
		newHistoryLogCmd(),
		newHistoryRestoreCmd(),
		newHistoryCleanCmd(),
		newHistoryGCCmd(),
	)
	return group
}

func newHistoryTransitionCmd(operation string) *cobra.Command {
	return &cobra.Command{
		Use: operation + " <file>", Short: "Apply the most recent " + operation + " history step",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := openStore()
			if err != nil {
				return err
			}
			var description string
			if operation == "undo" {
				description, err = repo.Undo(cmd.Context(), args[0])
			} else {
				description, err = repo.Redo(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", titleCase(operation), description)
			return err
		},
	}
}

func newHistoryLogCmd() *cobra.Command {
	return &cobra.Command{
		Use: "log <file>", Short: "List saved history steps", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := openStore()
			if err != nil {
				return err
			}
			entries, head, err := repo.Log(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for i, entry := range entries {
				marker := ""
				if i == head {
					marker = "HEAD"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d\t%s\t%s\n",
					entry.ID, entry.Timestamp.UTC().Format("2006-01-02T15:04:05Z"), entry.Snapshot.ByteSize,
					marker, entry.Description); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newHistoryRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use: "restore <file> <step-id>", Short: "Restore a saved history step",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, _, err := openStore()
			if err != nil {
				return err
			}
			description, err := repo.Restore(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Restored: %s\n", description)
			return err
		},
	}
}

func newHistoryCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "clean [file]", Short: "Remove persistent history",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all == (len(args) == 1) {
				return fmt.Errorf("provide one file or use --all")
			}
			repo, _, err := openStore()
			if err != nil {
				return err
			}
			if all {
				return repo.CleanAll(cmd.Context())
			}
			return repo.Clean(cmd.Context(), args[0])
		},
	}
	cmd.Flags().Bool("all", false, "Remove history for all files")
	return cmd
}

func newHistoryGCCmd() *cobra.Command {
	return &cobra.Command{
		Use: "gc", Short: "Remove unreferenced history blobs", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo, _, err := openStore()
			if err != nil {
				return err
			}
			return repo.GC(cmd.Context())
		},
	}
}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	return string([]byte{value[0] - ('a' - 'A')}) + value[1:]
}
