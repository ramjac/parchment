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

Settings are merged from built-in defaults, user TOML, workspace TOML, then
environment overrides. Workspace selection accepts `--workspace` and
`PARCHMENT_WORKSPACE`; user configuration can also set a workspace path and
parent-directory discovery behavior. `PARCHMENT_UNDO_LIMIT` overrides the
configured undo limit. Editor, theme, logging, and backup settings are parsed
but are not yet connected to runtime behavior, and their environment
variables currently have no effect.

## Interactive notes

Run `parchment tui` from a terminal. Use arrow keys or `j`/`k` to select notes,
`Enter` to preview, `n` to create, `e` to edit, `/` to search, `Enter` to submit
a search, `Esc` to clear search, `d` to request deletion, `?` for help, and `q`
to quit. `Ctrl+C` exits from the notes screen and cancels an operation in
progress; in the editor it cancels a clean edit but will not abandon unsaved
changes. Ctrl+C during a save cancels that operation and leaves the editor open.
In the editor, `Tab` switches between title and Markdown, `Ctrl+S` saves, and
`Esc` discards the edit. Deletion requires an explicit `y`; `n` or `Esc`
cancels. In the notes list, `u`/`Ctrl+Z` undo and `Ctrl+R` redo; these bindings
do not apply while editing. Use `Enter` to focus the preview in wide layouts
and `↑`/`↓` to scroll it; in narrow layouts, `Enter` opens the preview, `↑`/`↓`
scroll, and `Esc` returns to the notes list. Narrow and very small terminals use
a simpler layout and a minimum-size message; quit remains available. Preview is
plain text rather than rendered Markdown, so canonical Markdown is never
confused with presentation.

Undo and redo cover note creation, edits, title changes, tag changes, and
deletion. The bounded history is in memory for the current process and does
not survive a restart.

## Future work

This repository currently implements the notes slice. The broader suite is
planned to add:

- Working document, spreadsheet, presentation, and basic image-editing
  features using the shared workspace and artifact metadata.
- Shared artifact navigation and organization, including links and
  attachments, plus import and export.
- Backup operations and optional provider integrations. Any future Perkeep
  integration should remain optional and decoupled from core workspace logic;
  there is no custom synchronization protocol planned.
- A more complete interactive shell across artifact types, including
  contextual navigation and richer focus/key-binding behavior. Markdown
  rendering may be added as a presentation-only preview; Markdown remains the
  canonical note content.
- Runtime support for the currently parsed editor, theme, logging, and backup
  configuration settings.
- Persistent undo/redo, if introduced, with explicit storage and migration
  semantics. History currently lasts only for the running process.
- Make the default directory for storing Parchment artifacts a non-hidden folder and also make the default directory path configurable. Generally assume that Parchment artifacts might be read by other applications; especially text file and markdown interpreters.
- Background auto-save and recovery from auto-save so that in the event Parchment crashes or is force closed, any changes since the last save can be optionally recovered.
- Examples directory with examples of each of the types of artifacts.

## Development

```sh
gofmt -w cmd internal
go test ./...
go build ./cmd/parchment
```
