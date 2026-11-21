# Parchment

Parchment is a local-first terminal application for working with standalone
notes, documents, spreadsheets, presentations, and images. Each Markdown
artifact stays in the ordinary file you name and place; Parchment does not
require an account, workspace, or managed library.

## Features

- Native terminal editors and readers for Markdown notes, laid-out documents,
  formula-enabled spreadsheets, and slide presentations.
- File-based Markdown artifacts that remain inspectable and editable with
  ordinary tools.
- PDF printing and export, document ODT/DOCX interchange, spreadsheet CSV
  export, and standalone PNG/JPEG viewing and editing.
- Local recovery drafts and persistent undo/redo for saved artifact changes.

## Install

Parchment requires Go 1.26.8 or newer. Clone this repository, then install the
command from its module directory:

```sh
git clone https://github.com/ramjac/parchment.git
cd parchment
go install ./cmd/parchment
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`. Confirm the installation with
`parchment --help`.

## Documentation

- [User Guide](docs/User_Guide.md) — installation, commands, editors, exports,
  configuration, and the features shown in the examples.
- [Technical Guide](docs/Technical_Guide.md) — architecture and the Markdown
  artifact format, including the reserved code-block conventions.
- [Future Work](FutureWork.md) — remaining roadmap items and acceptance
  criteria.
- [Example artifacts](examples/) — complete note, document, spreadsheet, and
  presentation files.
