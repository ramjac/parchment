1. **Add `RemoveImage` to `document.Service` (`internal/document/document.go`)**:
   - Signature: `func (s *Service) RemoveImage(ctx context.Context, path, imageName string, stripReferences bool) (Document, error)`
   - Logic:
     - Fetch current document and its changes.
     - Check for pending changes mentioning the image.
     - If `!stripReferences`, check if the body mentions the image.
     - Strip image from document body if `stripReferences` is true.
     - Update the document to remove the image.

2. **Add CLI commands in `internal/cli/documents.go`**:
   - `document images`:
     - Print `name\tformat\twxh\tbytes\t(referenced|unreferenced)` for each embedded image.
   - `document image-extract`:
     - `image-extract <file> <image-name> -o <output> [--force]`
     - Atomically save the extracted image bytes to the given output path.
   - `document image-remove`:
     - `image-remove <file> <image-name> [--strip-references]`
     - Call `s.RemoveImage`.

3. **Update tests in `internal/cli/documents_test.go` and `internal/document/document_test.go`**.

4. **Complete pre-commit steps to ensure proper testing, verification, review, and reflection are done.**
