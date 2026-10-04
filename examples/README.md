# Artifact examples

These files show Parchment's canonical, inspectable artifact formats where
available. `budget.spreadsheet.json` is a complete workbook artifact, including
its metadata and formulas; it is not CSV. `image.png` is embedded in the
document. Standalone image artifact editing is not implemented yet.

From the repository root, run:

```sh
workspace=./example-workspace
parchment init "$workspace"

parchment --workspace "$workspace" note create "Field notes" \
  --body "$(cat examples/note.md)"
document_id=$(parchment --workspace "$workspace" document create "Project brief" \
  --body-file examples/document.md)
parchment --workspace "$workspace" document image "$document_id" examples/image.png
spreadsheet_id=0123456789abcdef0123456789abcdef
mkdir -m 700 -p "$workspace/.parchment/artifacts/$spreadsheet_id"
cp examples/budget.spreadsheet.json \
  "$workspace/.parchment/artifacts/$spreadsheet_id/spreadsheet.json"
chmod 600 "$workspace/.parchment/artifacts/$spreadsheet_id/spreadsheet.json"
parchment --workspace "$workspace" spreadsheet cell "$spreadsheet_id" D2
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
