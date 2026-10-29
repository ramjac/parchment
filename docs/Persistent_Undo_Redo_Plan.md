## 6. Plan persistent undo/redo storage format

Make a plan for persistent, bounded, per-file undo/redo history stored under
`~/.parchment/history/`. This work item is **planning only**; the implementation of
the persistent storage engine will follow this architectural design.

### Storage Strategy: Content-Addressable Blobs vs. Diffs
- **Evaluation of Text Diffs**: Parchment artifacts blend Markdown text with
  structured JSON payload fences (`parchment-*` code blocks) and binary data
  (base64 encoded images, spreadsheet cell models). Forward/backward unified
  diffs on structured and base64 content are fragile, computationally expensive to
  apply reliably across schema updates, and prone to corrupting binary blocks.
- **Evaluation of Monolithic Snapshots**: Storing full file duplicates for every
  keystroke or undo step rapidly exhausts disk space, especially for documents
  with embedded high-resolution images.
- **Recommended Architecture**: A hybrid content-addressable blob store with a
  per-artifact index manifest:
  - Snapshots capture the exact canonical artifact file bytes at each saved step.
  - Large binary payloads and unchanged file contents are hashed (SHA-256) and
    stored deduplicated in a shared blob pool (`~/.parchment/history/blobs/<sha256>`).
  - Identical images and unchanged blocks across multiple undo steps share the
    exact same underlying blob on disk.
  - Snapshot files and blobs are created with strict owner-only permissions (`0600`)
    and directories with `0700`.

### Directory Structure & Manifest Schema
History is organized beside the existing recovery store under `~/.parchment/`:
```
~/.parchment/
├── recovery/                    # Existing autosave recovery drafts
└── history/
    ├── blobs/                   # Deduplicated immutable content blobs (0600)
    │   ├── 4a8b1...
    │   └── e3b0c...
    └── <path-sha256>/           # Per-artifact history keyed by absolute path hash
        ├── index.json           # Stack manifest, pointer, and step metadata
        └── journal.json         # In-flight transition journal for crash recovery
```

**Manifest Schema (`index.json`)**:
- `schema_version`: Format version integer (e.g. `1`).
- `artifact_path`: Absolute path of the tracked artifact file.
- `undo_limit`: Maximum number of undo steps retained (default `100`, configured via `undo_limit` / `PARCHMENT_UNDO_LIMIT`).
- `head_index`: Zero-based index into `entries` pointing to the current state (-1 if empty).
- `entries`: Ordered array of history snapshots:
  - `id`: Monotonic step ID or UUID.
  - `timestamp`: UTC ISO-8601 timestamp.
  - `description`: Human-readable label (e.g. `"Edit text"`, `"Accept proposal"`, `"Delete column"`).
  - `file_digest`: SHA-256 hex digest of the artifact on disk at this state.
  - `blob_hash`: SHA-256 hex digest referencing the snapshot in `blobs/<blob_hash>`.
  - `byte_size`: File size in bytes.

### Crash Recovery & Transition Journaling
To prevent corruption if the process is terminated mid-transition (e.g., power loss
or SIGKILL between disk write and history pointer update), use a two-phase journaled
commit:
1. **Prepare**: Write `journal.json` atomically:
   `{"transition_id": "...", "from_digest": "...", "to_digest": "...", "target_head": N, "status": "pending"}`.
2. **Apply**: Atomically overwrite the artifact file with the target blob contents
   using `writeAtomic` and sync directory metadata.
3. **Commit**: Atomically update `index.json` with `head_index = target_head`.
4. **Finalize**: Remove `journal.json`.

**Startup / Reopen Resolution**:
- When opening an artifact, check if `journal.json` exists for its path hash.
- Compare the current disk file's SHA-256 digest against `from_digest` and `to_digest`:
  - If current file matches `to_digest`: The file write succeeded before `index.json` was committed. Commit `index.json` to `target_head` and delete `journal.json`.
  - If current file matches `from_digest`: The process crashed before writing the file. Delete `journal.json` (rollback).
  - If current file matches neither: An external modification occurred during an interrupted transition. Retain both file and history, display an explicit conflict warning, and refuse automated recovery.

### External Modification & Conflict Handling
- Before executing any `Undo()` or `Redo()` operation, compute the SHA-256 digest of
  the artifact file currently on disk and compare it with `entries[head_index].file_digest`.
- If the digests do not match:
  - An external edit occurred (e.g. file modified in an external editor or switched by Git).
  - **Do not overwrite the external edit.** Refuse the undo/redo operation and report:
    `"File modified externally; persistent history preserved for recovery."`
  - History remains intact and can be inspected or explicitly recovered if requested.

### Retention, Pruning & Garbage Collection
- **Branch Truncation**: When a new edit is saved while `head_index < len(entries)-1`,
  drop all subsequent redo entries (standard linear history model).
- **History Bounding**: When total entries exceed `undo_limit`, prune the oldest
  entries from `index.json`.
- **Blob Garbage Collection**: Unreferenced blobs in `~/.parchment/history/blobs/`
  are reclaimed via an automated or CLI-invoked garbage collection pass (`parchment history gc`),
  which builds the set of active `blob_hash` references across all `index.json` files
  and removes unreferenced blob files.
- **Purge CLI**: Support `parchment history clean <path>` to remove history for a
  specific file and `parchment history clean --all` to purge all persistent history.

### Service & Architecture Boundaries
- Define the history persistence abstraction in `internal/history`:
  ```go
  type Store interface {
      Load(ctx context.Context, path string) (*Stack, error)
      Save(ctx context.Context, path string, stack *Stack) error
      Record(ctx context.Context, path string, desc string, data []byte) error
      Undo(ctx context.Context, path string) ([]byte, string, error)
      Redo(ctx context.Context, path string) ([]byte, string, error)
      Purge(ctx context.Context, path string) error
  }
  ```
- Implement the storage engine in `internal/filerepo` beside `recovery.Store`.
- Keep domain services (`document`, `note`, `spreadsheet`, `presentation`) dealing
  strictly with high-level undo/redo operations, leaving file hashing, blob storage,
  and journal recovery inside the repository.

**Acceptance:** A fully documented, verified architectural plan for persistent undo/redo
is established covering storage schemas, blob deduplication, crash recovery journaling,
external edit conflict detection, and interface boundaries. Implementation is deferred
to follow this plan.
