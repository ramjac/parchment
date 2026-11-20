# Guidelines for AI Agents and GitHub Copilot

This file provides rules, architectural conventions, and constraints for AI agents
(such as Google Jules, Antigravity, and GitHub Copilot) working on the Parchment
codebase.

## Build, test, and format

Use Go 1.24 or newer.

- Build: `go build ./cmd/parchment`
- Run all tests: `go test ./...`
- Run tests with race detector: `go test -race ./...`
- Run one test: `go test ./internal/note -run '^TestNoteOperationsUndoAndRedo$'`
- Format Go code: `gofmt -w cmd internal`

There is no repository-specific lint target or linter configuration.

## Core principles

### 1. Artifacts are ordinary files in shared folders

Parchment does not and will never manage a directory of artifacts. A
Parchment artifact is an ordinary standalone file that lives wherever the user
puts it, such as the home directory or `~/Documents`, alongside the user's
other, non-Parchment files. Those folders are shared spaces, not Parchment
territory. The same artifact can be opened by Parchment or by any text editor.

- Do not create or require a Parchment-owned artifact directory, store,
  library, vault, collection, workspace, or similar concept under any name.
  Renaming the concept (for example an "artifacts directory") does not make it
  acceptable.
- "Separating Parchment files from the user's other files" is not a goal of
  this repository; the opposite is intended. Do not justify designs with it.
- Do not give artifacts opaque storage paths such as `<id>/content.md` or
  require Parchment-specific folder layouts, marker files, lock directories,
  or bookkeeping files beside artifacts. A user names and places their files.
- Do not scan, index, or take ownership of a directory to find artifacts;
  operate on the files the user explicitly opens or names.
- `~/.parchment/` is for Parchment's own configuration and application state
  only (`parchment.toml`, the global write lock, autosave recovery drafts,
  logs, and persistent undo/redo history). It never contains artifacts.
- Do not reintroduce `list`, `search`, or `delete` commands that treat a
  directory as Parchment's collection. Users find, rename, move, and delete
  artifact files with ordinary tools.

### 2. Never launch an external editor

- **Parchment will never launch an external editor (such as `vi`, `nano`, or `$EDITOR`)
  from within the application.**
- Do not implement TUI process suspension (such as `tea.ExecProcess`) or editor
  subprocess spawning.
- Parchment is designed as an all-in-one terminal productivity suite with its
  own native TUI editors and CLI commands. All in-app editing occurs strictly
  within Parchment's native TUI components.
- If an artifact exceeds editor limits or contains unsupported content, report
  a clean error to the user rather than shelling out to an external editor.

### 3. Local-first modular monolith

- The file being edited is authoritative. Normal operations must work completely
  offline without background daemons, accounts, or telemetry. Keep data
  inspectable and usable with ordinary filesystem tools and other editors.
  Do not claim exclusive ownership of user files or move them into private
  application storage. Never require `init` or a workspace flag to open a file.
- Services (`internal/<kind>`) own domain rules and operations.
- Repositories (`internal/filerepo`) own filesystem I/O, atomic writes, and locking.
- Presentation (`internal/tui`) owns UI state and renders views via Bubble Tea.
- CLI (`internal/cli`) builds Cobra commands.

### 4. Scope exclusivity

- In-document Markdown link navigation, heading slugs, explicit anchors, and
  the left-side outline navigation pane apply **strictly and exclusively to Documents**.
- They do **not** apply to Notes (which are flat, unstructured text notes) or
  Presentations (which use slide sequencing).

### 5. Markdown integrity

- Persisted files retain canonical Markdown; renderers hide reserved
  `parchment-*` blocks while preserving ordinary code fences and user prose.
- Never alter canonical Markdown during rendering, outline parsing, or link resolution.

## Architecture and technical reference

`cmd/parchment` is the executable entry point; `internal/cli` builds the Cobra
command tree and resolves settings. Services in `internal/<kind>` own domain
rules, `internal/filerepo` owns filesystem persistence, `internal/tui` owns
presentation, and the CLI and TUI orchestrate these layers rather than
duplicating domain behavior.

Keep the artifact path as the user-facing identity. Runtime IDs and titles are
derived from the path, not stored in the artifact or used as file addresses.
Plain Markdown without a Parchment envelope opens as a note and remains plain
when saved.

For the artifact envelope and structured blocks, Markdown parsing/rendering,
filesystem write and external-edit behavior, application-state layout, and
persistent history details, follow the [Technical Guide](docs/Technical_Guide.md).
For current commands and user workflows, see the [User Guide](docs/User_Guide.md).

## Repository-specific conventions

- Changes to `main` must only be integrated by merging a Pull Request on GitHub.
  Never commit directly to `main` or update it outside a GitHub Pull Request
  merge.
- Parchment has not had its first release and has no legacy artifacts or
  compatibility obligations. Do not add legacy-format support, migrations, or
  backwards-compatibility handling for artifact format changes. If format
  requirements change incompatibly, update the implementation and the
  checked-in files in `examples/`. Remove this instruction at the first release.
- Keep note behavior in the service layer and persistence in the filesystem
  repository; the CLI and TUI should orchestrate these rather than duplicate
  note rules.
- Keep architectural dependencies pointed outward: domain/application code
  must not depend on Cobra, Bubble Tea, terminal rendering, or provider-specific
  integrations. Resolve configuration at the CLI/application boundary; do not
  load environment or config files from domain packages.
- Use Cobra for the file-path CLI and typed TOML configuration; do not add Viper.
  Use the standard-library `log/slog` API if logging is implemented. Keep the
  CLI thin and make operations reusable by both CLI and TUI. Do not introduce
  ID-based commands for opening or editing files; IDs are internal metadata,
  not user-facing file addresses.
- Follow the visual and interaction contract in `docs/TUI_Design.md` for
  semantic styling, layout, focus, small-terminal behavior, state
  preservation, and visual evidence.
- Follow Bubble Tea's model/update/view/command structure. Keep views free of
  I/O and domain mutation; represent blocking operations as commands returning
  typed result messages. Keep key bindings contextual and ensure text editing
  and confirmation states receive input before background screens. Dispatch a
  pending multi-key shortcut before standalone bindings for its continuation
  key, so the continuation is not intercepted as an unrelated action. Do not
  introduce a universal child-component interface until multiple real
  components need it.
- Text editors in notes, documents, and presentations use a dedicated native
  editor widget (`internal/tui/editor`) with micro-style visual selections
  (`Lines [][]rune`, `Point {Line, Col}`, `Selection {Anchor, Active}`),
  rather than `bubbles/textarea`.
- Do not require a stored artifact ID or title to open, edit, or save a file.
  Runtime IDs are derived from paths; they must not dictate file names or
  folder layout, and operations address artifacts by path, not ID.
- Persist each artifact as one human-readable file; do not add per-artifact
  sidecars. The artifact format and filesystem persistence invariants are
  specified in the [Technical Guide](docs/Technical_Guide.md).
- Keep import/export formats separate from domain models.
- Document viewing and previews render Markdown via Glamour. Differentiate
  image presentation: in editor mode, embedded images appear as links or
  placeholders; in document preview and reader modes, images render as full
  graphical output (via Kitty, Sixel, or iTerm2 protocols with ANSI half-block
  fallback).
- A document or presentation may select one font for the entire artifact, not
  per section. Font selection applies to printer-oriented or rendered output
  only. Do not apply artifact font settings to terminal output: TUI and terminal
  previews use the font configured by the user's terminal emulator.
- Follow the Technical Guide's separation between parsing structured blocks
  and hiding reserved blocks during rendering. Preserve canonical Markdown;
  do not interpret a body block as structured data or mutate source as a
  rendering side effect.
- Artifact timestamps are UTC. Configuration is a single versioned TOML file
  (`version = 1`) at `~/.parchment/parchment.toml`, or the existing file named
  by `PARCHMENT_CONFIG`; environment overrides are applied afterward. Do not
  reintroduce workspaces, collections, or per-directory config files.
- Closing a note, document, spreadsheet, or presentation editor normally
  (confirmed when dirty) deletes the file's recovery draft; only an abnormal
  exit leaves one, which is offered when the same path is reopened. Documents
  support opt-in tracked Markdown edits:
  `F3` toggles tracking and `F4` reviews saved pending changes.
- Documents, spreadsheets, and presentations may open in their reader views;
  notes open directly in their editor. An existing document opens in its
  reader (outline pane beside the rendered pages); a newly created document or
  a restored recovery draft opens in the editor. Leaving the document editor
  returns to the reader. Do not remove reader views when changing editors.
- Keep undo operations meaningful and bounded; record only successful
  operations and clear redo after a new operation. See the Technical Guide for
  saved-file history persistence and retention behavior.
- Add packages only for demonstrated responsibilities; do not create empty
  placeholders, use Go's `plugin` package, or introduce a TUI framework besides
  Bubble Tea.
- Tests use Go's `testing` package and temporary directories for filesystem
  behavior. CLI tests construct a command with injected output streams; TUI
  tests exercise the Bubble Tea model with messages rather than starting an
  interactive terminal, using real services over `filerepo` in temporary
  directories; CLI tests point `HOME` at a temporary directory and use
  `t.Chdir`. Keep tests independent of a real home directory,
  network, timezone, terminal, and machine-specific configuration.
