# Artifact examples

Each sample is a complete, standalone Markdown artifact file. A leading
`parchment-meta` code block contains shared metadata; the document, spreadsheet,
and embedded image data is kept in hidden Parchment code blocks. The body
remains ordinary Markdown after the `<!-- parchment-body -->` separator.

The files can be opened where they are, or copied anywhere alongside your
other files. From the repository root:

```sh
parchment note show examples/note.md
parchment document print examples/document.md
parchment spreadsheet cell examples/budget.md D2
parchment presentation preview examples/presentation.md
parchment tui examples/document.md
```

The garden illustration is embedded in the document's hidden JSON payload as
base64, so the artifact is still one Markdown file.
