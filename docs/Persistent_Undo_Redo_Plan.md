# Persistent Undo/Redo Storage Plan

Implement persistent, bounded, per-file undo/redo history stored under
`~/.parchment/history/` as part of **Phase 5** (PRs 36–38, following Phase 4 in
[`FutureWork.md`](../FutureWork.md)). Persistent history replaces the
process-local in-memory `history.Stack` across domain services while keeping
unsaved keystroke undo/redo in `internal/tui/editor`.

## Storage Strategy: Chunked Content-Addressable Blobs

- **Why not text diffs or monolithic file copies:** Unified diffs are fragile
  across structured JSON `parchment-*` blocks and base64 binary payloads, while
  full-file copies duplicate multi-megabyte embedded images on every 1-byte
  prose edit.
- **Chunked Content-Addressable Architecture:**
  - Decompose each saved canonical artifact state into content-addressed
    SHA-256 blobs under `~/.parchment/history/blobs/<sha256>`:
    - For plain Markdown notes (without a `parchment-meta` envelope): store one
      raw file blob (`raw_blob_hash`).
    - For Parchment envelope artifacts: store the metadata envelope JSON, a
      `body_blob_hash` for the Markdown body, structured block blobs for each
      trailing `parchment-*` block, and—for `parchment-document` and
      `parchment-presentation`—individual `blobs/<sha256>` entries for each
      embedded image's raw binary `Data` (referenced by name and blob hash in
      the block descriptor rather than re-storing base64 image JSON on every
      text edit).
  - Reassembling a snapshot reconstructs the exact canonical artifact bytes and
    verifies them against the recorded full-file SHA-256 `file_digest` before
    writing to disk.
  - **Byte-Exact Reconstruction Fallback:** If decomposing a file's envelope
    and re-encoding it via `artifactfile.Encode` fails to produce the exact
    on-disk SHA-256 `file_digest` (e.g. due to non-canonical JSON whitespace or
    hand-edited comments in the envelope fences), record the snapshot using a
    single `raw_blob_hash` (matching plain Markdown note storage) so 100%
    byte-for-byte fidelity is guaranteed.
  - Identical bodies, blocks, and embedded images across any number of edits or
    undo/redo cycles share a single immutable blob on disk.
  - All history directories are created with owner-only `0700` permissions and
    all manifest, journal, and blob files with `0600` permissions.

## Directory Structure & Manifest Schema

History lives beside `recovery/` under `~/.parchment/` and is keyed by the
SHA-256 of the symlink-resolved canonical path (`filepath.EvalSymlinks` falling
back to `filepath.Abs` on the parent directory if needed, matching `filerepo`
symlink-target semantics):

```text
~/.parchment/
├── write.lock                   # Global lock coordinating artifact & history writes
├── recovery/                    # Existing autosave recovery drafts
└── history/
    ├── blobs/                   # Deduplicated immutable content chunks (0600)
    │   ├── 4a8b1...
    │   └── e3b0c...
    └── <canonical-path-sha256>/ # Per-file history directory (0700)
        ├── index.json           # Stack manifest, baseline, head pointer & chunk refs
        └── journal.json         # Two-phase transition journal for crash recovery
```

### Baseline Invariant & Manifest Schema (`index.json`)

- **Non-Undoable Baseline (`entries[0]`):**
  - Creating a new file records its initial created state as `entries[0]`
    (`description: "Initial state"`, `head_index: 0`). Because `head_index == 0`
    has no predecessor, `CanUndo()` is `false` (creating a file is never
    undoable, and Parchment never deletes artifact files).
  - When an existing untracked file (`len(entries) == 0`) is modified for the
    first time, `Record` atomically seeds `entries[0]` from the pre-edit disk
    bytes (`"Initial state"`) and appends the post-edit state as `entries[1]`
    (`head_index = 1`), ensuring `Undo()` can always restore `entries[head_index-1]`.
  - Plain Markdown notes without `parchment-meta` are tracked identically via
    `raw_blob_hash` and `file_digest`.
- **Manifest Fields:**
  - `schema_version`: `1`.
  - `artifact_path`: Canonical absolute path of the tracked file.
  - `undo_limit`: Maximum undo transitions retained (`entries` cap is
    `undo_limit + 1` so `undo_limit` undo steps remain available above the
    current floor entry; configured via `undo_limit` / `PARCHMENT_UNDO_LIMIT`).
  - `head_index`: Zero-based index into `entries` for the current state (`0` at
    floor; `CanUndo()` is `head_index > 0`; `CanRedo()` is
    `head_index >= 0 && head_index < len(entries)-1`).
  - `entries`: Ordered slice of snapshots:
    - `id`: Monotonic step ID (`uint64` string or compact hex ID).
    - `timestamp`: UTC RFC 3339 timestamp.
    - `description`: Human-readable label (e.g., `"Initial state"`,
      `"Update note"`, `"Accept document change"`, `"Delete column"`,
      `"External edit"`).
    - `file_digest`: Full-file SHA-256 hex digest of the canonical file on disk.
    - `byte_size`: Exact file byte length on disk.
    - `chunks`: Envelope reconstruction manifest (`raw_blob_hash` for plain
      Markdown, or `meta`, `body_blob_hash`, block blob hashes, and detached
      image `name -> blob_hash` map).

## Crash Recovery, Locking & Transition Journaling

All mutations to `index.json`, `journal.json`, `blobs/`, and artifact transitions
run under the global `~/.parchment/write.lock` so concurrent Parchment CLI/TUI
processes and garbage collection never race.

To prevent state drift if killed mid-undo/redo/restore, transitions execute a
two-phase journaled commit:
1. **Prepare:** Write `journal.json` atomically (`0600`):
   `{"transition_id": "...", "from_digest": "...", "to_digest": "...", "target_head": N}`.
2. **Apply:** Reconstruct target bytes from `blobs/`, verify `SHA-256 == to_digest`,
   and atomically write the artifact file (`writeAtomic` + directory sync,
   preserving permissions and symlink targets).
3. **Commit:** Atomically update `index.json` with `head_index = target_head`.
4. **Finalize:** Remove `journal.json`.

**Startup / Open Resolution:**
- When opening or operating on a file with a lingering `journal.json`, compute
  the current disk file's SHA-256 digest:
  - Matches `to_digest`: commit `index.json` (`head_index = target_head`) and
    remove `journal.json`.
  - Matches `from_digest`: rollback by removing `journal.json`.
  - Matches neither: retain both file and history, surface a conflict warning,
    and do not overwrite the file.

## External Modification & Conflict Handling

- **On `Undo()` or `Redo()`:** Verify that the current disk file's SHA-256
  equals `entries[head_index].file_digest`. If it differs, refuse the undo/redo
  and return:
  `"File modified externally; persistent history preserved for recovery."`
- **On Saving a New Edit After an External Modification:** When a user opens an
  externally modified file and saves a *new* change in Parchment while
  `disk_digest != entries[head_index].file_digest`, truncate any stale redo
  tail after `head_index`, record the externally modified pre-save disk file as
  an `"External edit"` checkpoint entry, and append the newly saved Parchment
  state after it. This preserves both the older history and the external edit
  without forcing the user to purge history first.

## Retention, Automatic GC & CLI Subcommands

- **Branch Truncation & Dynamic `undo_limit` Enforcement:**
  - Saving a new edit while `head_index < len(entries)-1` drops redo entries
    above `head_index`.
  - The currently configured `Settings.UndoLimit` always governs on file writes:
    whenever an artifact is saved or modified, compare the manifest's stored
    `undo_limit` with the active configuration. If they differ, update the
    manifest's `undo_limit`.
  - When `len(entries) > undo_limit + 1`, drop the oldest entries from `index.json`
    (the new `entries[0]` becomes the current non-undoable floor) and
    automatically delete any blob hashes from `blobs/` that are no longer
    referenced by any `<path-sha256>/index.json`. Reading or running
    `parchment history log` never mutates or prunes history.
- **CLI Commands (`parchment history`):**
  - `parchment history undo <file>`: Undo the most recent persisted step on
    `<file>` (enabling stateless CLI undo across invocations).
  - `parchment history redo <file>`: Redo the next persisted step on `<file>`.
  - `parchment history log <file>`: List recorded steps (`id`, UTC timestamp,
    byte size, current `HEAD` marker, and `description`).
  - `parchment history restore <file> <step-id>`: Atomically restore `<file>`
    to a specific recorded step (recording the pre-restore external state first
    if `<file>` was modified externally, while still using normal atomic write
    safeguards).
  - `parchment history clean <file>` / `parchment history clean --all`: Remove
    persistent history for one file or all files and garbage-collect orphaned
    blobs.
  - `parchment history gc`: Scan all `index.json` manifests (under
    `write.lock`) and delete any unreferenced files in `blobs/`.

## Service, TUI & Repository Boundaries

- **Recording History in Repository Writes:**
  - Define `history.Store` in `internal/history` and implement it in
    `internal/filerepo` alongside `recovery.Store`.
  - To prevent state drift and guarantee atomicity, persistent history entries
    are recorded directly inside `filerepo`'s locked write operations
    (`Transition*` and file creation): every successful atomic write invokes
    `store.Record(canonicalPath, preWriteBytes, postWriteBytes, description)`
    under `write.lock`.
- **Domain Services & TUI:**
  - Services (`note.Service`, `document.Service`, `spreadsheet.Service`,
    `presentation.Service`) receive `history.Store` from `filerepo`, pass
    operation descriptions (e.g. `"Update note"`, `"Accept change"`,
    `"Delete row"`) through to repository write calls, and expose
    `Undo(ctx, path)` / `Redo(ctx, path)` that delegate to `history.Store` under
    `write.lock`.
  - Keep `internal/tui/editor`'s keystroke `undoStack` strictly for unsaved
    in-memory typing; saving (`Ctrl+S`) resets the editor's unsaved keystroke
    stack so subsequent `Ctrl+Z` / `Ctrl+R` delegates cleanly to `history.Store`.

## Step-by-Step PR Breakdown

Each step below is scoped as one self-contained Pull Request (`~250–500` LOC)
that compiles cleanly and passes `go test -race ./...`:

1. **PR 36a — `history.Store`, Raw Blob Store & Crash Journal in `filerepo`:**
   - Define `history.Store` and entry/log types in `internal/history`.
   - Implement `filerepo` raw blob persistence under `~/.parchment/history/`:
     canonical-path SHA-256 directory, `<path-sha256>/index.json` manifest
     (`entries[0]` baseline seeding, full-file `raw_blob_hash` storage),
     `write.lock` coordination, and two-phase `journal.json` prepare/apply/commit
     crash recovery.
   - Wire history recording directly into `filerepo`'s atomic write path. Test
     stateless commit/rollback, journal crash resolution, and concurrent locks.
2. **PR 36b — Envelope Chunk Decomposition & Exact Fallback:**
   - Extend history recording to decompose Parchment envelope files into
     individual `body_blob_hash`, structured block blobs, and detached embedded
     image blobs under `blobs/<sha256>`.
   - Verify reassembled snapshot bytes against `file_digest`; if re-encoding
     differs (e.g. hand-edited envelope whitespace), automatically fall back to
     storing `raw_blob_hash`. Test chunk deduplication with multi-megabyte images
     across repeated edits.
3. **PR 37 — Service & TUI Integration + External-Edit Checkpointing:**
   - Replace in-memory `*history.Stack` across `note.Service`,
     `document.Service`, `spreadsheet.Service`, and `presentation.Service` with
     `history.Store` (including `CanUndo`/`CanRedo` queries across restarts in
     TUI screens), remove obsolete in-memory `Operation` wrappers, refuse
     `Undo`/`Redo` when modified externally, and record an `"External edit"`
     checkpoint when saving new changes atop an externally modified file. Test
     cross-restart TUI undo/redo and external-modification workflows across all
     four artifact kinds.
4. **PR 38 — `parchment history` CLI & Automatic Blob Garbage Collection:**
   - Implement `parchment history undo`, `redo`, `log`, `restore`,
     `clean [<file> | --all]`, and `gc` in `internal/cli`, plus dynamic
     `undo_limit` pruning on write and automatic orphaned-blob reclamation.
     Test stateless CLI undo/redo/restore across invocations, `undo_limit`
     shrink pruning, symlink-target identity sharing, and blob GC safety.

**Acceptance:** Persistent undo/redo ships cleanly across three focused PRs,
survives restarts across both TUI and CLI for all artifact kinds (including
plain Markdown notes), deduplicates body/block/image chunks, recovers from
interrupted transitions, preserves external edits safely, and passes unit and
race-detector tests in temporary `HOME` directories.
