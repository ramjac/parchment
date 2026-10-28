1. **Analyze `TestDecodeTrailingBlockErrorPaths`**
   - It's a table-driven test that has grown slightly large, grouping several different error scenarios (unclosed fences, duplicate blocks, invalid json, invalid prefixes).
   - I will extract the setup and assertion logic into a small test helper `assertDecodeTrailingBlockError` (or similar) to reduce duplication.
   - I will split the large table into smaller, more focused test functions grouped by the type of error they are testing (e.g., `TestDecodeTrailingBlock_DuplicateBlocks`, `TestDecodeTrailingBlock_InvalidFences`, `TestDecodeTrailingBlock_InvalidContent`).
2. **Implement Refactoring**
   - Create the helper `assertDecodeTrailingBlockError`.
   - Create `TestDecodeTrailingBlock_UnclosedFence`
   - Create `TestDecodeTrailingBlock_DuplicateBlocks`
   - Create `TestDecodeTrailingBlock_InvalidContent`
   - Create `TestDecodeTrailingBlock_InvalidFences`
3. **Run Pre-Commit Checks**
   - Run tests using `go test ./...` and `go test -race ./...`.
   - Format using `gofmt -w ...`.
4. **Submit PR**
   - Include description outlining the code health improvements.
