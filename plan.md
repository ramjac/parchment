1. **Analyze the Issue**:
    - The prompt complains about `Repo.TransitionSpreadsheet` being too long and starting with `func (r *Repo) TransitionSpreadsheet(ctx context.Context, sourcePath, targetPath string, target *spreadsheet.Spreadsheet, formatBlocks map[string]any) error`.
    - However, looking at the code in the repository right now (`internal/filerepo/spreadsheets.go`), `TransitionSpreadsheet` is `func (r *Repository) TransitionSpreadsheet(ctx context.Context, path string, expected, target *spreadsheet.Spreadsheet) error` and is fairly short (around 33 lines).
    - It's likely that the prompt text is showing a slightly older version of the codebase or a different branch that I don't have.
    - Let's assume the prompt actually means the *current* `TransitionSpreadsheet` on the current branch (`internal/filerepo/spreadsheets.go`). It's currently:
      ```go
      func (r *Repository) TransitionSpreadsheet(ctx context.Context, path string, expected, target *spreadsheet.Spreadsheet) error {
          if target == nil {
              return errors.New("Parchment does not delete artifact files")
          }
          if err := validateTarget(target.Artifact, artifact.SpreadsheetKind); err != nil {
              return err
          }
          path, err := filepath.Abs(path)
          if err != nil {
              return err
          }
          return r.update(ctx, path, expected == nil, spreadsheet.ErrNotFound, func(content []byte) ([]byte, error) {
              if expected != nil {
                  if err := checkKind(path, content, artifact.SpreadsheetKind); err != nil {
                      return nil, err
                  }
                  current, err := decodeSpreadsheet(path, content)
                  if err != nil {
                      return nil, err
                  }
                  want := *expected
                  want.Path = path
                  if !spreadsheet.Equal(current, want) || target.ID != current.ID {
                      return nil, changedError(path)
                  }
              }
              data, err := spreadsheet.Encode(*target)
              if err != nil {
                  return nil, fmt.Errorf("encode spreadsheet: %w", err)
              }
              return data, nil
          })
      }
      ```
    - The logic can be refactored by extracting the inner closure into its own function, or by extracting the validation logic.
    - Let's extract the closure to something like `checkAndEncodeSpreadsheet(path string, expected, target *spreadsheet.Spreadsheet, content []byte) ([]byte, error)`.

2. **Refactoring Plan**:
    - Extract the inner function of `r.update` in `TransitionSpreadsheet`. Let's name it `verifyAndEncodeSpreadsheet` or similar.
    - Update `TransitionSpreadsheet` to use this new function.
    - Ensure tests pass.

3. **Execution**:
    - Create a plan.
    - Extract function in `internal/filerepo/spreadsheets.go`.
    - Run tests: `go test ./internal/filerepo`.
    - Pre-commit steps.
    - Submit.
