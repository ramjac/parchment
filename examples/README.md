# Artifact examples

Each sample is a complete, standalone Markdown artifact file. A leading
`parchment-meta` code block contains shared metadata. The ordinary Markdown
body follows `<!-- parchment-body -->`; hidden `parchment-*` data blocks, such
as document layout, workbook cells, and embedded images, follow the explicit
`<!-- parchment-blocks -->` marker after the body.

Open any example where it is, without setup:

```sh
parchment examples/note.md
parchment examples/document.md
parchment examples/budget.md
parchment examples/presentation.md
parchment document print examples/document.md
parchment spreadsheet cell examples/budget.md D2
```

Edits save to the opened file. Copy an example elsewhere first if you want to
keep the checked-in sample unchanged. The garden illustration is embedded in
the document's hidden JSON payload as base64, so the artifact is still one
Markdown file.
