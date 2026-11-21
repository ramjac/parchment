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

## Large-artifact performance and responsiveness

Extend the Go benchmarks with reproducibly generated inputs rather than
committing huge fixtures. Record baseline commands and results, and agree on
latency and memory targets before optimization. Measure opening, editing,
rendering/scrolling, saving, and peak memory at fixed terminal sizes; verify
that improvements preserve content, layout, and view-state stability.

- **Documents:** Hundreds of pages and several hundred megabytes of embedded
  images; measure page navigation, Markdown layout/rendering, image decoding,
  and memory retained while scrolling.
- **Presentations:** Hundreds of slides and several hundred megabytes of media;
  measure slide switching, source editing, preview rendering, and image/cache
  memory without sacrificing responsive keyboard input.
- **Spreadsheets:** Hundreds of rows and columns with dense data, formulas, and
  dependencies; measure bulk edits/paste, dependent recalculation, grid
  scrolling, and saving, including large update cascades and cycle errors.
