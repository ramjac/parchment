1. **Understand**: We have a nested loop comparing `draft.Images` against `before.Images` in `Service.Propose` (`internal/document/document.go:107`).
2. **Measure**: I have already written a benchmark `BenchmarkProposeImages` which gives a baseline.
3. **Implement**:
   - Replace the nested loop with a map approach.
   - We will create `existingImages := make(map[string][]byte, len(before.Images))` and populate it.
   - Then, loop over `draft.Images`, checking the map for a matching name and comparing the `Data` using `bytes.Equal`.
4. **Verify**:
   - Rerun `BenchmarkProposeImages` to verify the performance improvement.
   - Run formatting and linting tools (`go test ./...`).
   - Run `pre_commit_instructions` as required by the directives.
5. **Present**:
   - Create a PR using `submit` tool with title and description detailing the performance improvement.
