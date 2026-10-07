# Artifact examples

Each sample is a complete Markdown artifact file. A leading
`parchment-meta` code block contains shared metadata; the document, spreadsheet,
and embedded image data is kept in hidden Parchment code blocks. The body
remains ordinary Markdown after the `<!-- parchment-body -->` separator.

Copy the sample files into a workspace:

```sh
workspace=./example-workspace
parchment init "$workspace"
for id in 10000000000000000000000000000001 \
          20000000000000000000000000000002 \
          30000000000000000000000000000003 \
          40000000000000000000000000000004; do
  mkdir -m 700 -p "$workspace/artifacts/$id"
done
cp examples/note.md "$workspace/artifacts/10000000000000000000000000000001/content.md"
cp examples/document.md "$workspace/artifacts/20000000000000000000000000000002/content.md"
cp examples/budget.md "$workspace/artifacts/30000000000000000000000000000003/content.md"
cp examples/presentation.md "$workspace/artifacts/40000000000000000000000000000004/content.md"
chmod 600 "$workspace"/artifacts/*/content.md
```

Inspect each artifact type in the sample workspace with:

```sh
parchment --workspace ./example-workspace note list
parchment --workspace ./example-workspace document list
parchment --workspace ./example-workspace spreadsheet list
parchment --workspace ./example-workspace presentation list
```

Try the spreadsheet formula and document print preview:

```sh
parchment --workspace ./example-workspace spreadsheet cell 30000000000000000000000000000003 D2
parchment --workspace ./example-workspace document print 20000000000000000000000000000002
```

The garden illustration is embedded in the document's hidden JSON payload as
base64, so the artifact is still one Markdown file.
