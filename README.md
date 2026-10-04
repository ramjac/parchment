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
`presentation`, and `image` kinds. Notes and documents have application
behavior; the other kinds are not yet implemented.

### Documents

Documents are a separate feature from notes, stored the same way but with
multi-page printing in mind. A document artifact directory contains
`metadata.json` (kind `document`), the canonical Markdown `content.md`, an
optional `layout.json` (page size, orientation, margins in millimeters, column
count, header, footer, page-number placement), and any embedded images as
`image-<hash>.png|jpg|gif`, referenced from the Markdown with ordinary image
links. Page and section breaks are HTML comments so other Markdown tools ignore
them: `<!-- parchment:page-break -->` and
`<!-- parchment:section-break columns=2 [continuous] -->`. Headers and footers
are `left|center|right` text with `{title}`, `{page}`, and `{pages}` tokens.

Printing renders monospaced pages (10 characters and 6 lines per inch) with
margins, columns, headers, footers, and page numbers, separated by form feeds:

```sh
parchment document create "Report" --body-file report.md --columns 2 --footer "{title}|{page}/{pages}"
parchment document print <id> | lpr
```

Document commands (`parchment document`, alias `doc`): `list`, `create`,
`show`, `edit`, `rename`, `tag-add`, `tag-remove`, `layout`, `page-break`,
`section-break`, `image`, `print`, `search`, and `delete --yes`. In the
interactive shell, press `Tab` on the notes screen to switch to documents (and
back). The document editor always shows a toolbar above the text with buttons
for save, preview, close, text formatting (bold, italic, strikethrough, code,
headings, lists, quote, link, rule, image) and page setup (page and section
breaks, columns, margins, page size, orientation, header, footer, page
numbers). Click a button, press `F2` and use the arrow keys with `Enter`, or
use the `Alt` shortcuts (`B` bold, `I` italic, `C` code, `1`–`3` headings,
`L`/`N` lists, `Q` quote, `K` link, `M` image, `P` page break, `S` section
break). `F5` previews the printed
pages and `Ctrl+S` saves. Document changes, including images, support undo
and redo like notes.

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

This repository currently implements notes and documents. The broader suite is
planned to add:

- Change tracking for documents (recording, reviewing, accepting, and
  rejecting edits).
- Working spreadsheet features using the shared workspace and artifact metadata.
- Working presentation features using the shared workspace and artifact metadata.
- Basic image-editing features using the shared workspace and artifact metadata.
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
