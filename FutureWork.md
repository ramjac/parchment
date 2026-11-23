# Future Work

This document lists open or undecided work only. Completed implementation
phases and merged PR checklists are intentionally omitted. See the
[User Guide](docs/User_Guide.md) for current behavior and the
[Technical Guide](docs/Technical_Guide.md) for architecture and format details.

## Cross-platform support and 1.0 releases

Make ordinary Windows-authored Markdown usable on every supported platform,
and prepare native Linux, macOS, and Windows releases for 1.0.0.

- **Validate native platform support:** Build and run tests on macOS and
  Windows as well as Linux, then exercise CLI and interactive workflows in
  their terminals. Verify paths and configuration, file locking and atomic
  saves, recovery and history, terminal input and rendering, and clipboard
  behavior. Add macOS and Windows CI and document supported environments and
  any remaining limitations.
- **Ship all three platforms by 1.0.0:** Decide which architectures to support,
  produce and verify Linux, macOS, and Windows release packages and checksums,
  and document installation and any platform-specific requirements. Keep
  existing beta releases Linux-only until the additional packages are ready.

## Long-document reader loading and rendering

Investigate reducing the work and memory needed to open long documents. The
reader can show a bounded provisional full-screen preview before indexing,
then progressively render roughly 500-source-line batches with a loading
indicator and a bounded scrolling viewport, also used after loading. Section
jumps can complete as their target is rendered; End and whole-document search
wait for the entire document. Eligible documents, including those with images
and charts, retain a checkpoint index and a rendered window after loading;
distant navigation and rendered-text search run in the background. Loading
still retains the complete render until it finishes; oversized indexes and
other ineligible inputs retain it afterward. Width changes repeat the full
render; position restoration remains to be designed.
For eligible input, the document-root layout streams completed Markdown blocks,
reducing peak memory during rendering.
Rendered output, source block size, non-streaming fallback size, expanded
embedded Markdown, chart output and retained scaled images have explicit limits.
Images are prepared as their blocks are reached, but chart and sheet references are expanded before
streaming. Image reads and spreadsheet evaluation check cancellation, but
parsing, expanded block buffers, spreadsheet evaluation state and temporary
image decoding are not yet fully bounded or cancellable. A conservative
preflight rejects some oversized non-streamable sources before parsing, including
safe-to-check link rewrites; HTML and embedded references still use the later
limit check.
In an exploratory five-run Linux pseudo-terminal measurement at 80x24, the
3.3 MB *War and Peace* fixture took a median 3.86 seconds to open and reached
156 MiB peak process RSS. A sampled CPU profile pointed mainly to full-document
Markdown rendering. The model benchmark allocated about 620 MB cumulatively
per open, which is not peak live memory. See
[Performance Baselines](docs/Performance_Baselines.md) for the existing
workloads and measurement limitations.

- Evaluate incremental or on-demand loading, parsing, and rendering, and
  bounded caching or prefetching, rather than assuming a particular design.
  Separate the cost of file I/O, indexing, layout, and rendering; consider
  responsiveness during initial display, later navigation, and resize.
- Preserve canonical Markdown and existing reader behavior, including the
  outline, duplicate-heading slugs, anchors, internal links, whole-document
  find, embedded media and charts, and navigation to distant sections.
  Handle Markdown constructs and references that cross rendering boundaries,
  preserve view position across reflow, and retain existing external-edit
  handling.
- Compare against the novel fixture and smaller documents at multiple terminal
  sizes. Measure time to first usable view, CPU, peak RSS and allocations,
  plus scrolling, outline jumps, search, and resizing; verify rendered content
  and navigation against existing behavior. Record repeatable results and
  trade-offs before choosing performance targets or an implementation.

## Expanding the maximum usable file size

Investigate supporting files larger than the current 16 MiB Markdown artifact
and plain-note file cap. This cap is an application-defined safeguard, not a
Markdown or Go limit; raising the constant alone does not establish that
larger files are usable. The complete encoded artifact counts toward the cap,
including metadata and embedded data. Other independent limits can be reached
sooner, including the native editor's text limits described below.

Exploratory Linux PTY tests used temporary documents made by repeating the
saved *War and Peace* body. With the former 64 MiB rendered-output limit,
16, 32, and 60 MiB documents showed initial content but later failed on
rendered output; a 96 MiB document also hit the separate expanded-content
limit when the artifact cap was temporarily raised. Rendered terminal output
can be much larger than source Markdown because it includes styling and
wrapping. Both reader limits are now 256 MiB, independently of the 16 MiB
file cap.

With the higher reader limits, the 16 MiB document finished rendering and
navigated End/Home. Three reconstructed 80x24 first-screen samples were
0.533, 0.464, and 0.495 seconds; a separate full-render run peaked at about
690 MiB process RSS with `GOMEMLIMIT=1GiB`. These are fixture-specific,
shared-machine observations, not physical-terminal measurements or general
size guarantees. See [Performance Baselines](docs/Performance_Baselines.md)
for the setup and other reader limits.

- Measure time to the first useful full screen separately from full rendering,
  alongside navigation responsiveness and peak RSS. Test larger and varied
  files, including media and structured content, rather than only repeated
  text; identify the particular limit or resource that fails in each case.
- Review the file, rendered-output, expanded-content, line, block, media, and
  editor limits together. Change limits only with evidence, clear errors, and
  safe handling of source files; verify opening, editing where supported,
  saving, recovery, and history at and above each boundary.
- Investigate memory and rendering costs where measurements justify it.
  Retaining a complete render is not itself evidence of a problem; evaluate
  any alternatives against responsiveness and correctness.
- Keep source-position and reopen-position restoration as separate design
  work; do not assume an implementation before that feature is discussed.

## Spreadsheet edit, recalculation, and save

Investigate the cost of a single cell edit in large workbooks. An earlier
exploratory model benchmark on the mortgage-and-budget fixture measured about
151 ms and 84 MB cumulatively for editing an input, recalculating, and saving.
After reducing scalar-formula work during spill discovery and streaming
aggregate calculation, the same saved-fixture workflow measured a 50.3 ms
median and 47.6 MB/op over three single-iteration benchmark runs. Allocations
are cumulative totals, not peak live memory; opening and remaining save-path
costs still warrant separate profiling.

- Profile recalculation, rendering, serialization, history, and atomic
  persistence separately to determine where the remaining time and allocation
  go.
- Evaluate recalculating only affected dependents and reducing unnecessary
  workbook copying or re-encoding, rather than assuming a particular design.
- Preserve formula results, cross-sheet references, span semantics, cycle and
  depth errors, undo/redo and persistent history, and canonical file contents.
- Continue measuring scrolling separately from sheet switching, and verify
  results against independently calculated mortgage and budget values.

## Presentation save cost

Investigate why saving the 251-slide history presentation after a small
source edit took about 86 ms and allocated about 48 MB cumulatively in
exploratory benchmarks; the service-level save alone was similar.

- Determine whether unchanged embedded images, chart data, or other structured
  blocks are copied, decoded, re-encoded, or rewritten unnecessarily during
  save, history recording, or recovery drafts.
- Preserve single-file atomic writes, canonical Markdown, external-edit
  detection, and history behavior; compare decks with and without media.

## Embedded media memory

Measure real application memory while navigating image-heavy documents and
presentations before optimizing. Existing benchmarks reported about 140-153
MiB test-process RSS, which includes fixture setup and does not isolate the
running application.

- Measure peak and retained RSS in a fresh process while paging through media,
  using the ANSI half-block fallback and, where practical, Kitty, Sixel, and
  iTerm2 output.
- Examine decode, scaling, and protocol-encoding costs, cache retention and
  eviction, and images near the per-artifact encoded-size limit.
- Verify rendered output and clean refusal of oversized artifacts.

## Raising text editor size limits

The shared native text editor refuses text over 1,000,000 UTF-8 bytes or
10,000 lines. That prevents opening long plain-text files as notes; for
example, a 3.36 MB, 66,032-line plain-text *War and Peace* cannot be opened.
The limits exist because several editor costs grow with the whole buffer on
every edit, rather than with the size of the change. Investigate removing
those costs, then raise the limits based on measurements while keeping an
explicit cap that protects memory.

- **Undo and redo:** Each edit currently stores a full copy of the buffer as
  runes (about 4 bytes per character), and up to the undo limit of copies is
  retained. For a file of this size that could reach gigabytes. Evaluate
  recording only each change, with grouping of related keystrokes, while
  preserving cursor and selection restoration, line-ending preservation,
  redo clearing, and the undo limit.
- **Per-keystroke size checks:** Inserting text rebuilds the full buffer to
  check its byte size and counts lines across it. Evaluate maintaining
  running byte and line counts so checks are proportional to the edit.
- **Unsaved-change detection:** Dirty checks compare the full buffer with the
  saved text. Evaluate a change counter or saved-revision marker, keeping
  correct results when edits are undone back to the saved state.
- **Buffer representation:** Evaluate whether the line-of-runes buffer should
  change (for example to a piece table or rope) so edits, selections, and
  clipboard operations avoid copying unrelated text, without changing the
  editor's visual-selection behavior or introducing a non-native editor.
- **Whole-text operations:** Recovery drafts, save, find, Markdown preview,
  and persistent history currently process the entire text. Measure them at
  larger sizes and evaluate incremental, bounded, or deferred approaches,
  including preview rendering only what is visible, consistent with the
  long-document reader work.
- **Consistent limits:** The limits are enforced in several places, including
  `internal/notetext/text.go`, the editor's insertion check, and the TUI's
  unpreservable-text check. Define them once and apply them consistently to
  notes and to documents and presentations that use the same editor; keep
  clear refusal messages for text over the new limits without altering the
  source.
- **Validation:** Add near-limit and over-limit note workloads, including the
  plain-text novel, and measure open, editing at the beginning, middle and end,
  undo/redo, find, preview, autosave, and save, with CPU, peak RSS, and
  allocations. Confirm canonical text, line endings, and encoding round-trip
  unchanged, and record results before choosing new limits.

## Startup with unresponsive terminals

When a terminal does not answer capability queries (the background-color OSC
11 query and the DA1 device-attributes query), startup waited about 4 seconds
before showing any artifact in pseudo-terminal measurements. With replies,
every artifact type except the novel opened in about 0.3 seconds.

- Identify which real terminals, multiplexers, remote sessions, and CI or
  automation environments fail to answer these queries.
- Evaluate skipping, shortening, or deferring the queries, or applying
  detected values asynchronously, without breaking theme selection, keyboard
  enhancement detection, or image protocol selection.
- Measure time to first usable view with responsive and unresponsive
  terminals.
