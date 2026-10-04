# Artifact examples

Each sample is a complete Markdown artifact file. A leading
`parchment-meta` code block contains shared metadata; the document, spreadsheet,
and embedded image data is kept in hidden Parchment code blocks. The body
remains ordinary Markdown after the `<!-- parchment-body -->` separator.

Copy the sample files into a workspace:

```sh
workspace=./example-workspace
parchment init "$workspace"
for id in n1 d2 s3 p4; do
  mkdir -m 700 -p "$workspace/parchment/artifacts/$id"
done
cp examples/note.md "$workspace/parchment/artifacts/n1/content.md"
cp examples/document.md "$workspace/parchment/artifacts/d2/content.md"
cp examples/budget.md "$workspace/parchment/artifacts/s3/content.md"
cp examples/presentation.md "$workspace/parchment/artifacts/p4/content.md"
chmod 600 "$workspace"/parchment/artifacts/*/content.md
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
parchment --workspace ./example-workspace spreadsheet cell s3 D2
parchment --workspace ./example-workspace document print d2
```

The garden illustration is embedded in the document's hidden JSON payload as
base64, so the artifact is still one Markdown file.
