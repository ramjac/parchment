# Artifact examples

Each sample is a complete Markdown artifact file. A leading
`parchment-meta` code block contains shared metadata. The ordinary Markdown
body follows `<!-- parchment-body -->`; hidden `parchment-*` data blocks, such
as document layout and embedded images, follow the explicit
`<!-- parchment-blocks -->` marker after the body.

Open any example directly, without a workspace or setup:

```sh
go run ./cmd/parchment examples/note.md
go run ./cmd/parchment examples/document.md
go run ./cmd/parchment examples/budget.md
go run ./cmd/parchment examples/presentation.md
```

Edits save to the opened file. Copy an example elsewhere first if you want to
keep the checked-in sample unchanged.
