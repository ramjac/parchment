# Parchment

Parchment is a local-first terminal editor for files wherever they live. A
user's ordinary filesystem is the shared workspace: Parchment and other
editors can open the same files. It works offline without an account, server,
Parchment-specific directory, or initialization step. Requires Go 1.24 or
newer.

## Open a file

```sh
go run ./cmd/parchment path/to/notes.md
go run ./cmd/parchment examples/note.md
go run ./cmd/parchment examples/document.md
go run ./cmd/parchment examples/budget.md
go run ./cmd/parchment examples/presentation.md
```

The path is the only command argument. Files are saved back to that path;
Parchment does not move them into a managed directory or create a project.
Ordinary Markdown remains ordinary Markdown on save. A Parchment artifact
starts with a `parchment-meta` fenced JSON block containing its kind, format
version, creation time, and modification time (no title or ID). Optional
editable Markdown follows a `<!-- parchment-body -->` separator. Optional
kind-specific `parchment-*` blocks follow the body after an explicit
`<!-- parchment-blocks -->` marker. Metadata, workbook cells, document layout,
embedded images, and change history stay in the same inspectable file, with
the human-readable content before large structured payloads.

Notes open directly in the Markdown editor (`Ctrl+S` saves; `Esc` discards
unsaved edits or quits when clean).
Documents offer a paginated preview and a body-first editor (no separate
title field), layout, images, and proposals. Spreadsheets open as a grid:
use arrow keys to select a cell,
`Enter` to edit, `=` to enter a formula, and `u`/`Ctrl+R` for undo/redo.
Presentations open as slides with an editable Markdown source. Press `q` to
quit an editor. Existing file permissions are preserved on save; edits made
by another program while a file is open are not overwritten.

## Configuration

The optional per-user `parchment.toml` lives under the OS configuration
directory at `parchment/parchment.toml` (for example,
`~/.config/parchment/parchment.toml` on Linux). It is not stored beside an
edited file. A minimal configuration is:

```toml
version = 1
undo_limit = 100
```

Settings are loaded from built-in defaults, then the per-user file, then
environment overrides. Undo history is in memory for the current editor
session only. Files are written via a temporary file, sync, and rename.

## Future work

Parchment supports notes, documents, basic spreadsheets, and Markdown
presentations. Planned work includes:

- Background autosave and optional recovery of unsaved changes after a crash
  or forced close.
- File navigation and organization, including Markdown links.
- Backup operations and optional provider integrations. Any Perkeep
  integration should remain optional; no custom synchronization protocol is
  planned.
- Handling for commonly used editing key combinations like Ctrl+del to remove whole words. Ctrl+arrow for moving the cursor word by word. Tab for inserting several spaces or a tab.  Also Shift+arrow keys for selections.
- Overall visual improvements
  - Notes editor doesn't use full height of terminal
  - Parchment app components at the bottom of the page like the "Ready" status and undo/redo indicators are not visually separated from the content of the artifact being shown. The same is true for the app components at the top of the page.
- Runtime support for parsed settings such as editor, theme, logging, and
  backup configuration.
- Persistent undo/redo, if introduced, with explicit storage and recovery
  semantics. History currently lasts only for the running process.
- Make a plan for importing and exporting to/from common document file
  formats. Acknowledge that this will be a "lossy" process in the sense that
  Parchment doesn't support many of the features of those formats, like
  multiple fonts or exact image/object positioning.
  - Export to ODF file format
  - Export to DOCX file format
  - Import from ODF file format
  - Import from DOCX file format
- Basic image-editing features for files opened directly by Parchment.
  - Crop
  - Resize

## Development

```sh
go build ./cmd/parchment
go test ./...
```

The module path `example.com/parchment` is a placeholder until the project
selects a canonical module path.
