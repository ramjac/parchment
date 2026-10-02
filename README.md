# parchment

`parchment` is the beginning of a local-first productivity workspace for the
terminal. The first working slice is Markdown notes: workspace storage, shared
artifact metadata, direct filesystem search, a command-line interface, and an
interactive notes screen.

The module path `example.com/parchment` is a placeholder because this
repository does not yet define a canonical Go module path.

## Requirements

- Go 1.24 or newer
- A filesystem writable by the current user

Normal use is offline. There are no accounts, hosted services, telemetry,
background processes, or synchronization protocol.

## Quick start

```sh
go run ./cmd/parchment init ~/Documents/parchment
go run ./cmd/parchment --workspace ~/Documents/parchment note create "First note" \
  --body "Markdown stays readable on disk."
go run ./cmd/parchment --workspace ~/Documents/parchment note list
go run ./cmd/parchment --workspace ~/Documents/parchment tui
```

From inside an initialized workspace, the `--workspace` flag can be omitted:
parchment searches the current directory and its parents. `PARCHMENT_WORKSPACE`
can select a workspace explicitly. Use `parchment --help` and
`parchment note --help` for the complete command tree.

Available note operations include `note create`, `list`, `show`, `edit`,
`rename`, `add`, `remove`, and `delete`. Deletion requires `--yes`. The
`search <query>` command scans note titles, Markdown bodies, and tags directly
from the workspace; there is no search index or daemon.

## Storage

The workspace is an ordinary directory. `parchment.toml` contains versioned
workspace configuration; `.parchment/artifacts/<stable-id>/` contains each
artifact's `metadata.json` envelope and canonical `content.md`. Metadata
timestamps are UTC RFC 3339 values. Notes remain readable and editable with
ordinary filesystem tools. Copying, archiving, or versioning the workspace
with standard tools is sufficient for a local backup.

Initial artifact metadata recognizes the `note`, `document`, `spreadsheet`,
`presentation`, and `image` kinds. Only notes have application behavior in
this milestone; the other kinds are not yet implemented.

## Configuration

User configuration is read from the platform's standard user configuration
directory at `parchment/config.toml`. Workspace configuration lives at
`parchment.toml`. Both use TOML and require `version = 1`. For example:

```toml
version = 1
editor = "vi"
theme = "adaptive"
undo_limit = 100

[workspace]
discovery = "parents"

[logging]
level = "warn"
format = "text"

[backup.local]
destination = ""
```

The effective setting precedence is command-specific flags, persistent CLI
flags, environment variables, workspace TOML, user TOML, then built-in
defaults. Workspace selection accepts `--workspace` and `PARCHMENT_WORKSPACE`.
The currently supported environment overrides are `PARCHMENT_EDITOR`,
`PARCHMENT_THEME`, `PARCHMENT_UNDO_LIMIT`, `PARCHMENT_LOG_LEVEL`, and
`PARCHMENT_LOG_FORMAT`.

## Interactive notes

Run `parchment tui` from a terminal. Use arrow keys or `j`/`k` to select notes,
`Enter` to preview, `n` to create, `e` to edit, `/` to search, `Enter` to submit
a search, `Esc` to clear search, `d` to request deletion, `?` for help, and `q`
to quit. `Ctrl+C` exits from the notes screen; in the editor it cancels a clean
edit but will not abandon unsaved changes. In the editor, `Tab` switches between
title and Markdown, `Ctrl+S` saves, and `Esc` discards the edit. Deletion
requires an explicit `y`; `n` or `Esc` cancels. `u`/`Ctrl+Z` undo and `Ctrl+R`
redo. Narrow and very small terminals use a simpler layout and a minimum-size
message; quit remains available. Preview is plain text rather than rendered
Markdown, so canonical Markdown is never confused with presentation.

Undo and redo cover note creation, edits, title changes, tag changes, and
deletion. The bounded history is in memory for the current process and does
not survive a restart.

## Development

```sh
gofmt -w cmd internal
go test ./...
go build ./cmd/parchment
```
