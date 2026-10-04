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

Pull requests targeting `main` run `go build ./cmd/parchment` and `go test ./...`
in separate GitHub Actions jobs (`Go build` and `Go test`). The `main` branch
requires pull requests and both checks to pass before merging.

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
workspace configuration; artifacts live under
`.parchment/artifacts/<stable-id>/content.md`. Every supported artifact is one
Markdown file. It begins with a `parchment-meta` fenced code block containing
the shared metadata as JSON. Structured artifact data uses additional
`parchment-<thing>` JSON code blocks before the visible Markdown body. Parchment
renderers hide these reserved blocks while ordinary Markdown remains readable.
Metadata timestamps are UTC RFC 3339 values. Copying, archiving, or versioning
the workspace with standard tools is sufficient for a local backup.

Initial artifact metadata recognizes the `note`, `document`, `spreadsheet`,
`presentation`, and `image` kinds. Notes, documents, spreadsheets, and basic
presentations have application behavior; image editing is not yet implemented.

### Documents

Documents are a separate feature from notes, with multi-page printing in mind.
The visible body is canonical Markdown. A hidden `parchment-document` JSON
block stores page layout, embedded image bytes, and proposal history in the
same `content.md` artifact file. Embedded image references use ordinary
Markdown links. Page and section breaks are HTML comments so other Markdown
tools ignore them: `<!-- parchment:page-break -->` and
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
`section-break`, `image`, `print`, `search`, `propose`, `changes`, `review`,
`accept`, `reject`, and `delete --yes`. `propose` records a title, Markdown,
or layout edit without changing the live document; `review` displays the
current and proposed Markdown, and `accept` or `reject` resolves the pending
proposal. Only one proposal may be pending per document, and a proposal cannot
be accepted if the live document has changed since it was recorded. For
proposals, existing embedded images can be kept or removed, but new image data
must be added with the regular `image` command. For example:

```sh
change=$(parchment document propose <id> --body-file revised.md --description "Revise introduction")
parchment document review <id> "$change"
parchment document accept <id> "$change"
```

Proposal history is stored as inspectable JSON in each document's hidden
`parchment-document` block. In the interactive shell, press
`Tab` on the notes screen to switch to documents (and back). Press `c` on a
selected document to record a proposed edit, `v` to browse its changes, then
`Enter` to review and `a` or `r` to accept or reject a pending proposal. The
document editor always shows a toolbar above the text with buttons
for save, preview, close, text formatting (bold, italic, strikethrough, code,
headings, lists, quote, link, rule, image) and page setup (page and section
breaks, columns, margins, page size, orientation, header, footer, page
numbers). Click a button, press `F2` and use the arrow keys with `Enter`, or
use the `Alt` shortcuts (`B` bold, `I` italic, `C` code, `1`–`3` headings,
`L`/`N` lists, `Q` quote, `K` link, `M` image, `P` page break, `S` section
break). `F5` previews the printed pages and `Ctrl+S` saves (or records a
proposal when editing a proposal). Document changes, including images, support
undo and redo like notes.

### Spreadsheets

Spreadsheets are stored as one Markdown file per workbook:
`.parchment/artifacts/<id>/content.md`. The hidden `parchment-spreadsheet`
JSON block contains the versioned workbook data, named sheets, two-dimensional
cell arrays, and explicit literal or formula cells. This keeps the workbook
self-contained and avoids formulas being inferred from arbitrary text. CSV
files can initialize a workbook; CSV cells are imported as literal text. A new
workbook starts with `Sheet1`; additional sheets can be created with
`spreadsheet add-sheet`.

```sh
parchment spreadsheet create "Budget" --csv-file budget.csv
parchment spreadsheet cell <id> B2 12
parchment spreadsheet cell <id> C2 '=B2*2' --formula
parchment spreadsheet cell <id> C2
parchment spreadsheet show <id>
```

Formula evaluation currently supports numeric constants, same-sheet A1 cell
references, parentheses, unary signs, and `+`, `-`, `*`, and `/`. Empty
referenced cells evaluate to zero. Cycles, non-numeric references, and
division by zero are rejected. Formulas are limited to 4096 bytes and 512
nested dependencies or parentheses. Rows and columns can be inserted with
`spreadsheet insert-row` and `spreadsheet insert-column`; formula references
shift with inserted rows and columns. Spreadsheet edits can be undone and
redone through the service API while its process is running; the CLI is
stateless across invocations. The Markdown file with its JSON data block is
Parchment's canonical format, not a CSV file that third-party spreadsheet
applications can open directly.

### Presentations

A presentation is stored as one Markdown text file at
`.parchment/artifacts/<id>/content.md`. Its leading `parchment-meta` block
embeds the shared artifact metadata; the rest is editable Markdown. The initial
syntax follows Go present's Markdown conventions: `#` gives the deck title,
`##` begins a slide, `###` adds a subsection, `//` begins an ignored comment,
and `: ` begins a speaker-note line. Speaker notes and comments remain in the
source but are omitted from the plain-text preview.

```markdown
# Product Update

Presenter Name

## What changed

- Faster search
- Local-first storage

: Mention the upcoming release date.

## Questions

Thank you.
```

Use `presentation create <title> --body-file slides.md` to add a deck,
`presentation show <id>` to inspect its source, and
`presentation preview <id>` for a slide-separated plain-text preview.
`presentation edit <id> --body-file slides.md` replaces the Markdown source.
The parser preserves Markdown for later rendering but does not yet render it;
embedded media and Go present command directives are not implemented.

## Examples

The [`examples/`](examples/) directory contains complete Markdown artifact
files for notes, documents, spreadsheets, and presentations. Its README shows
how to copy them into a sample workspace; the document's image data is embedded
in its hidden payload.

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

This repository currently implements notes, documents, basic spreadsheets,
and basic Markdown presentations.
The broader suite is planned to add:

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

## Development

```sh
gofmt -w cmd internal
go test ./...
go build ./cmd/parchment
```
