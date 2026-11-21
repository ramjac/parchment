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

### Linux amd64 binary

Download and verify the [0.5.0 beta release](https://github.com/ramjac/parchment/releases/tag/v0.5.0-beta.1):

```sh
curl -fLO https://github.com/ramjac/parchment/releases/download/v0.5.0-beta.1/parchment_v0.5.0-beta.1_linux_amd64.tar.gz
curl -fLO https://github.com/ramjac/parchment/releases/download/v0.5.0-beta.1/checksums.txt
sha256sum -c checksums.txt
tar -xzf parchment_v0.5.0-beta.1_linux_amd64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 parchment "$HOME/.local/bin/parchment"
```

Ensure `~/.local/bin` is on your `PATH`, then run `parchment --help`.
This is a beta: artifact formats may change without backward-compatibility
guarantees. Back up your files before using it or upgrading.

### Build from source

Building from source requires Go 1.26.8 or newer. Clone this repository, then
install the command from its module directory:

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
