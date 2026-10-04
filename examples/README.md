# Artifact examples

These files are starter content for the artifact commands; Parchment creates
the actual artifacts, including their metadata, in the workspace. The
spreadsheet CSV is imported as literal values, then a formula is added with the
CLI. `image.png` is embedded in the document. Standalone image artifact
editing is not implemented yet.

From the repository root, run:

```sh
workspace=./example-workspace
parchment init "$workspace"

parchment --workspace "$workspace" note create "Field notes" \
  --body "$(cat examples/note.md)"
document_id=$(parchment --workspace "$workspace" document create "Project brief" \
  --body-file examples/document.md)
parchment --workspace "$workspace" document image "$document_id" examples/image.png
spreadsheet_id=$(parchment --workspace "$workspace" spreadsheet create "Monthly budget" \
  --csv-file examples/budget.csv)
parchment --workspace "$workspace" spreadsheet cell "$spreadsheet_id" D2 '=B2-C2' --formula
parchment --workspace "$workspace" presentation create "Product Update" \
  --body-file examples/presentation.md
```

Inspect each artifact type in the sample workspace with:

```sh
parchment --workspace ./example-workspace note list
parchment --workspace ./example-workspace document list
parchment --workspace ./example-workspace spreadsheet list
parchment --workspace ./example-workspace presentation list
```

The document's embedded image is stored alongside its Markdown content.
