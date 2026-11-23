# Preliminary large-artifact baselines

These are exploratory measurements, not responsiveness targets. Bubble Tea
model benchmarks do not measure terminal transport or human input. Physical
terminal latency, graphical image protocols, near-limit images, and many
editing positions remain to be measured. Allocations are not peak memory.

## Inputs

- **Document:** Leo Tolstoy, *War and Peace*, translated by Louise and Aylmer
  Maude, Project Gutenberg ebook 2600. Project Gutenberg lists the work as
  public domain in the USA; check local law before use. The standalone
  `internal/tui/testdata/war-and-peace.md` contains the complete UTF-8 text
  of [ebook 2600](https://www.gutenberg.org/ebooks/2600), updated June 14,
  2022, including its attribution, contents and full license. The supplied
  original has 3,359,405 bytes and SHA-256
  `e4bcf9042609b62c7de72a6f1b311f54c412943a9d641b7efcf79a464b5f31c8`.
  The artifact normalizes CRLF to LF, removes the UTF-8 BOM, adds a notice
  linking to the original, and marks the 15 books and two epilogues as level
  1 headings and their 365 chapters as level 2 headings. The printed contents
  remains unchanged. The artifact has 3,295,282 bytes and SHA-256
  `745c3694521d8462acf42d2311fcf644170288f875d617e9859ed458dd9fdd22`;
  it does not need to be regenerated or downloaded for the benchmarks.
- **Spreadsheet:** `internal/tui/testdata/mortgage-budget.md` is a standalone
  Parchment spreadsheet with a 360-month, $300,000 mortgage at 6%, plus three
  200-account monthly department budgets and a cross-sheet summary. The
  artifact has 610,324 bytes and SHA-256
  `03a54c18b9388a132b399dbcdc075feada5e3d065ccee9660e77a2c2b4e31a19`;
  it is saved in the repository and is not regenerated for the benchmarks. The
  payment factor is precomputed for that fixed rate and term; it is not a
  formula that adjusts when the rate changes. Tests check the final balance,
  a rollup, changes to inputs, and a circular dependency.
- **Note:** The README, User Guide, and Technical Guide joined in that order
  with one extra LF between files. At revision
  `1e654c2c90311afdb90652f0ac9a4e01d6777f10`, the files respectively had
  2,313, 24,688, and 15,284 bytes and 60, 489, and 286 LF characters. The
  result was 42,287 bytes,
  with 837 LF characters and SHA-256
  `44d17f83e7be00758ac45cb901cd9b163042f97e0e199d7f9803d36af973b164`.
  The benchmark reads the current checkout, so its contents and size may change.
- **Presentation:** `internal/tui/testdata/us-history.md` is a standalone
  Parchment presentation with 251 slides, one for each year from 1776 through
  2026. Each slide has an event and a short original paragraph; every third
  has three additional bullet points. Every fifth has an original, symbolic
  illustration, except every tenth has a graph instead: 83 lists, 25 embedded
  PNGs, and 25 editable embedded charts. Counting starts with 1776; the
  application also displays a separate title page.

  The graphs show population context, **not event-specific statistics**, using
  data from [OpenStax, U.S. History, United States Population Chart](https://openstax.org/books/us-history/pages/f-united-states-population-chart).
  They show the adjacent decade counts, except the 2025 slide uses the last
  two available censuses. The 1780 number is an estimate; census coverage and
  national boundaries changed over time. Each slide's notes link to a pinned
  Wikipedia year-page revision; these are starting points for further research,
  not citations for every detail. The history is not comprehensive or
  independently expert-reviewed. The 843,205-byte artifact has SHA-256
  `dfd69bc75d9dec28d222eadf6b4b8fb965ab9cf53139b9668e682d58e728fd26`.
- **Media:** Eight deterministic 512x512 PNG images per document or deck,
  8,400,222 encoded bytes for the document and 8,399,920 for the deck. The
  uncompressed RGBA pixels total 8 MiB. These are separate from the text
  workloads. Measurements force ANSI half-block fallback at 80x24 rather than
  Kitty, Sixel, or iTerm2. Existing artifact-file tests exercise rejection
  above the encoded-file limit, now 16 MiB.

## Measurements

Linux amd64, Go 1.27.1, Intel Core i7-1265U, source revision above plus the
uncommitted benchmark changes. Tests ran concurrently on a shared machine;
filesystem cache and other activity were not controlled. Values are medians
of three runs, rounded, and are indicative only.

| Component operation | Median time | Approximate bytes allocated/op |
| --- | ---: | ---: |
| Full novel repository load, warm file | 31.2 ms | 17.1 MB |
| Mortgage final balance evaluation, fresh evaluator | 12.8 ms | 8.4 MB |
| Department budget rollup, fresh evaluator | 9.5 ms | 8.8 MB |
| Guide note editor load | 206 µs | 208 KB |
| Guide note editor view, 80x24, middle | 150 µs | 9.2 KB |
| Guide note editor view, 120x40, middle | 306 µs | 19.1 KB |
| Guide note editor find | 176 µs | 4.1 KB |
| Guide note editor insert and undo | 278 µs | 583 KB |

```sh
go test ./internal/filerepo -run '^$' -bench '^BenchmarkLoadNovelDocument$' -benchmem -benchtime=1x -count=3
go test ./internal/spreadsheet -run '^$' -bench '^BenchmarkMortgageBudget$' -benchmem -benchtime=100ms -count=3
go test ./internal/tui/editor -run '^$' -bench '^BenchmarkGuideNoteEditor$' -benchmem -benchtime=100ms -count=3
```

The document benchmark loads the saved artifact, not document reader rendering.
The editor sizes are widget dimensions, not a full terminal session. The
spreadsheet benchmarks calculate individual cells with a new evaluator on
each iteration, not the complete grid or a persisted workbook. Fixture setup
is outside the reported timings.

## Workflow measurements

The following are medians of three single-iteration runs with
`GOMEMLIMIT=512MiB`, on the same shared machine. These short and sometimes
compound operations are indicative, not a latency distribution. Setup is
outside timed sections except where explicitly included. File cache was warm
and filesystem activity was uncontrolled.

| Workflow operation | Median time |
| --- | ---: |
| Note: model open and view, 80x24 | 1.82 ms |
| Note: preview, scroll and return, 80x24 / 120x40 | 23.37 / 24.14 ms |
| Note: edit and save | 2.53 ms |
| Presentation: service load | 31.63 ms |
| Presentation: navigate and render three slides, 80x24 / 120x40 | 4.20 / 3.40 ms |
| Presentation: navigate five mixed-content slides, 80x24 | 23.36 ms |
| Presentation: open source editor / edit and save / service save | 0.488 / 80.60 / 85.75 ms |
| Spreadsheet: model open, 80x24 | 30.35 ms |
| Spreadsheet: scroll, 80x24 / 120x40 | 1.33 / 1.76 ms |
| Spreadsheet: switch sheets and return, 80x24 / 120x40 | 7.49 / 8.34 ms |
| Spreadsheet: service edit, recalculate and save / cycle rejection | 50.27 / 15.67 ms |
| Media: in-memory document reader / render two presentation slides, 80x24 | 18.58 / 4.51 ms |
| Novel: in-memory reader, 80x24 / 120x40 | 2.544 / 2.634 s |
| Novel: scroll, view and return, 80x24 / 120x40 | 0.859 / 1.697 ms |
| Novel: navigate and render three outline positions, 120x40 | 5.11 ms |
| Novel: excerpt edit and save | 12.48 ms |

Full-novel model open-to-reader samples, including repository load, had a
median of 3.711 seconds and allocated about 619.5 MB per operation. A single
600-cell spreadsheet paste and recalculation took 25.24 ms and allocated
17.8 MB. Allocation totals include cumulative allocations, not live memory.

The full novel exceeds the editor's 1,000,000-byte and 10,000-line limits;
the benchmark checks clean refusal without changing the source. Its editable
excerpt uses the first 4,500 lines of the document body. The in-memory reader
case excludes file loading; the scrolling
case starts with the reader already constructed. Spreadsheet edit/save above
uses the service, not a simulated grid edit. None of the model benchmarks
include terminal I/O.

The presentation measurements were repeated after adding lists and media. Novel
measurements were repeated after adding headings; their benchmark and load
samples ran concurrently with other tests. Other workflow numbers are from the
earlier run. The updated presentation samples also ran on the shared machine,
concurrently with another test.

```sh
PARCHMENT_BENCH_MEDIA=1 GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^(BenchmarkNovelDocumentScreen|BenchmarkGuideNoteWorkflow|BenchmarkLargeSpreadsheet|BenchmarkHistoryPresentation|BenchmarkEmbeddedMedia)$' -benchmem -benchtime=1x -count=3
go test ./internal/spreadsheet -run '^$' -bench '^BenchmarkMortgageBudget/PasteAndRecalculate$' -benchmem -benchtime=1x -count=1
```

## Process memory

Linux `/usr/bin/time -v` reports maximum resident set for the entire fresh
test process, including fixture generation, Go runtime and setup, not just
the timed operation. These single samples are not memory targets.

| Scenario | Peak RSS (KiB) |
| --- | ---: |
| Novel in-memory reader, 80x24 (earlier, without headings) | 171,128 |
| Guide note preview, 80x24 | 31,288 |
| Spreadsheet open, 80x24 | 32,396 |
| History presentation edit/save (earlier, shorter slides) | 25,420 |
| Media document reader, 80x24 | 156,236 |
| Media presentation, 80x24 | 144,576 |

Build a standalone test binary, then run each selector in a separate process
from the package directory (the guide fixture reads the checkout relative to
that directory). Use absolute paths:

```sh
go test -c -o /absolute/path/tui-benchmark.test ./internal/tui
cd internal/tui
PARCHMENT_BENCH_MEDIA=1 GOMEMLIMIT=512MiB /usr/bin/time -v /absolute/path/tui-benchmark.test -test.run '^$' -test.bench '^BenchmarkNovelDocumentScreen/Reader80x24$' -test.benchmem -test.benchtime=1x -test.count=1
```

Substitute `^BenchmarkGuideNoteWorkflow/Preview/80x24$`,
`^BenchmarkLargeSpreadsheet/Open80x24$`,
`^BenchmarkHistoryPresentation/EditSave$`,
`^BenchmarkEmbeddedMedia/DocumentReader80x24$`, or
`^BenchmarkEmbeddedMedia/PresentationSlide80x24$` for the selector to measure
the other scenarios.

## Manual terminal check

From the repository root, export standalone files into an existing,
user-chosen directory with none of the four fixture names already present:

```sh
PARCHMENT_BENCH_EXPORT_DIR=/absolute/existing/path go test ./internal/tui -run '^TestExportPerformanceFixtures$' -count=1 -v
```

The repository artifacts can also be opened directly at
`internal/tui/testdata/war-and-peace.md`,
`internal/tui/testdata/mortgage-budget.md`, and
`internal/tui/testdata/us-history.md`;
use copies when testing edits.

At 80x24 and 120x40, open each file with `parchment /absolute/path/file`.
Record terminal and protocol, machine conditions, elapsed open/save times and
visible delays or failures; these checks have not been timed in the baseline.
Use disposable exported copies for edits.

1. In `guide-note.md`, type at several positions, use `Ctrl+F`, `F5` preview,
   scroll and `Ctrl+S`; reopen to check the saved text.
2. In `novel-document.md`, scroll and navigate sections in the reader, then
   press `e` and confirm the clean oversized-editor error and unchanged file.
3. In `mortgage-budget.md`, scroll, switch sheets with `Tab` / `Shift+Tab`,
   edit an input with `Enter`, and save with `Ctrl+S`; reopen and check results.
4. In `us-history.md`, use arrows, `Home` and `End` to navigate the deck.
   Check slide 15 for bullets and an illustration, and slide 30 for bullets
   and a chart. Press `e` to edit source, save with `Ctrl+S`, then reopen
   to check that the embedded media remains intact.

## Reader investigation: initial optimizations

The following comparisons are exploratory medians of three single-iteration
runs on Linux amd64, Go 1.27.1, Intel Core i7-1265U, with
`GOMEMLIMIT=512MiB`. The machine and warm file cache were not controlled.
The before build used the staged source before the reader changes; the after
build includes reader-only outline indexing, deferred editor initialization,
visible-only find highlighting, and cancellable background search for large
documents. Allocation figures are cumulative, not peak memory. The model
cases use an empty theme except where noted.

| Novel operation | Before | After | Before / after allocated |
| --- | ---: | ---: | ---: |
| Model open, 80x24 | 3.445 s | 3.235 s | 619.6 / 543.6 MB |
| In-memory reader, 80x24 | 2.573 s | 2.414 s | 569.9 / 493.7 MB |
| Reader outline, without print pagination | 449 ms | 337 ms | 88.8 / 53.6 MB |
| Rendered-text search to completion, 80x24 | 413 ms | 75 ms | 174.2 / 13.0 MB |
| Large search dispatch and view, 80x24 | synchronous | 1.42 ms | n/a / 0.18 MB |
| Resize 80x24 to 120x40 and back | 4.811 s | 4.680 s | 1,037 / 959 MB |

The same comparison for a small synthetic document with headings, links,
code and a table gave 2.006 / 1.827 ms at 60x24, 2.215 / 2.320 ms at
80x24, and 3.450 / 3.212 ms at 120x40 (before / after). An eight-image
document using ANSI fallback gave 34.250 / 42.858 ms, 35.028 / 36.299 ms,
and 41.232 / 38.178 ms respectively. These short samples are noisy; they
do not establish a small-document or media performance improvement.

After those comparisons, navigation-marker lookup was changed from repeated
outline scans to indexed queues. Three further single-iteration novel
`ReaderRender` samples had medians of 2.277 s before and 2.128 s after,
with approximately 492 MB allocated in both cases. This uncontrolled
comparison does not establish a reliable speedup or a memory reduction.
Later small-document samples had medians of 0.900, 1.009 and 1.532 ms at
60x24, 80x24 and 120x40; image-heavy samples had medians of 16.945, 17.653
and 19.026 ms. The large shift from earlier samples illustrates the
uncontrolled variability and should not be attributed to a specific change.

These are not equivalent to terminal latency. A single after-run of the
in-memory reader with a dark theme and TrueColor allocated about 1,148 MB
and took 6.535 s; a CPU profile was dominated by Glamour's renderer and
its wrapping, padding and ANSI writers.

In a separate Linux pseudo-terminal check at 80x24 with a temporary home,
`TERM=xterm-256color`, and no terminal capability replies, three baseline
samples detected the first visible `Gutenberg` text at 15.035, 16.336 and
15.160 seconds, with sampled peak RSS of 485,264, 485,444 and 484,936 KiB.
Three after samples took 14.934, 15.136 and 15.102 seconds, with sampled
peak RSS of 398,140, 340,512 and 396,860 KiB. Each process accepted `q` and
exited successfully after the text appeared. These samples include terminal
startup and process memory, not just rendering; the harness does not measure
input latency, physical terminals, cold disk, or graphical protocols. Machine
activity was uncontrolled.

After the marker-lookup change, three further pseudo-terminal runs took
14.720, 14.766 and 14.746 seconds, with sampled peak RSS of 396,980,
386,660 and 396,396 KiB. Each accepted `q` after content appeared.

With background rendering, a conservative initial-block preview, and
nonblocking terminal-background detection, the same harness reported first
content at 0.301, 0.223 and 0.330 seconds. It immediately quit after that
content, so its 43–47 MiB sampled peaks do **not** measure memory through
completion and are not comparable with the earlier completed-render peaks.
The in-memory model first frame had a 1.34 ms median over three samples,
excluding file loading; the corresponding open-to-completed-render median
was 3.229 s and allocated 542 MB. The preview now includes up to 16 safe
initial blocks (at most 8 KiB); unsupported first blocks show a pending
indicator instead of an unverified preview.
A separate single pseudo-terminal run sent `G` as soon as the first content
appeared: the end became visible at 3.211 seconds and sampled peak RSS was
397,576 KiB. This confirms that queued End navigation completed, not that
the whole render or its memory use is bounded.
After adding cancellation checkpoints, three first-content samples were
0.644, 0.653 and 0.554 seconds; a separate queued-End sample reached the end
at 8.236 seconds with 398,132 KiB sampled peak RSS. These runs show
substantial machine variability.

```sh
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/(Outline|ReaderOutline)$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/(StripPrivateBlocks|ReaderOutline)$' -benchmem -benchtime=100ms -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/ReaderRender$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/(ReaderRender|ReaderRenderCancellable)$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/EmbeddedExpansion$' -benchmem -benchtime=100ms -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelNavigationMarkers$' -benchmem -benchtime=100ms -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelDocumentScreen/(OpenReader80x24|Reader80x24|ReaderSearch80x24|ReaderSearchDispatch80x24|ReaderResize80x24To120x40)$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelDocumentScreen/(OpenReader80x24|ModelReaderFirstFrame)$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelDocumentScreen/ReaderDark80x24$' -benchmem -benchtime=1x -count=1
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkSmallDocumentReader$' -benchmem -benchtime=100ms -count=3
PARCHMENT_BENCH_MEDIA=1 GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkEmbeddedMedia/DocumentReader' -benchmem -benchtime=1x -count=3
go build -o /absolute/path/parchment-benchmark ./cmd/parchment
go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment-benchmark internal/tui/testdata/war-and-peace.md
go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment-benchmark internal/tui/testdata/war-and-peace.md --end
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkReaderSectionLookup$' -benchmem -benchtime=100ms -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelDocumentScreen/ScrolledReaderEditorRefusal' -benchmem -benchtime=1x -count=3
```

Large documents still render and retain all lines in one background operation
at open and on width changes. While it runs, input is handled with a bounded
queue and resize requests are coalesced, but navigation waits for completion.
Private-block stripping checks cancellation during its scan,
embedded-reference expansion checks between references, and Glamour's tree
walk checks between registered nodes. Individual chart or node renders and
the whole-document fallback layout remain non-interruptible. The streaming
path checks cancellation between tree nodes and flushes completed blocks.
Search still scans all rendered lines, and no bounded on-demand rendering
cache is implemented.
Large-document search retains at most 1,048,576 matches and reports an error
instead of presenting a partial count when that limit is exceeded.
After bounding the search results and storing only rows for reader navigation,
three novel-search samples had a 34.5 ms median and allocated 12.9 MB;
dispatch-and-view had a 0.48 ms median. Conditions remain uncontrolled.
Separately, allocating 382 navigation markers against the novel took a
70.3 ms median with repeated whole-source collision checks and 4.08 ms with
a single source scan and indexed checks (three samples, 100 ms benchmark
time). This isolates marker lookup; it is not an end-to-end rendering result.
Reusing the reference-collection AST when the full Markdown source needs no
rewriting reduced median `ReaderRender` allocations in another three-sample
comparison from about 491.7 MB to 464.6 MB; median time was 2.341 / 2.220 s.
This remains a full-document render and timings are uncontrolled.
With node-level cancellation enabled, three render samples took 1.971, 1.974
and 2.006 s, versus 1.995, 2.019 and 2.010 s without it; both allocated about
465 MB. These uncontrolled samples do not establish a performance target.
Skipping reference expansion when the novel contains no embedded references
reduced the isolated expansion median from 8.54 ms and 4.36 MB allocated to
2.73 ms and zero allocations (three uncontrolled samples).
When no reserved-block prefix exists, private-block stripping now returns the
original source. Its isolated median fell from 13.64 ms and 16.8 MB allocated
to 1.33 ms and zero allocations; the reader-outline median allocation fell
from 53.7 MB to 37.0 MB (three uncontrolled samples).
Streaming outline lines rather than retaining all split runs reduced the
outline median allocation further to 30.4 MB, with a 348 ms median. Subsequent
cancellable full-render samples allocated about 420 MB each and took 2.045,
2.324 and 2.181 s; timing remains variable and full rendering is still required.
Avoiding unnecessary inline conversion and regular-expression matches in the
outline pass reduced its median to 30.7 ms and 2.44 MB allocated. Subsequent
cancellable full-render samples took 1.678, 1.711 and 1.715 s and allocated
about 392 MB each. These comparisons are uncontrolled and do not measure peak
memory or establish targets.
Passing the reader's source directly to the formatter and skipping link
rewriting when no link opener is present reduced the next three full-render
samples to 363 MB allocated each, with a 1.632 s median.
A later pseudo-terminal End run showed first content at 0.267 s, End at
6.276 s, and sampled peak RSS of 403,056 KiB. Terminal styling and workload
conditions differ from the in-memory benchmark.
A later queued-End pseudo-terminal run showed first content at 0.215 s, End
at 6.611 s, and sampled peak RSS of 416,820 KiB.
After extending the initial preview to multiple safe blocks, a separate
queued-End run showed first content at 0.332 s, End at 6.799 s, and
397,484 KiB sampled peak RSS. The in-memory first-frame samples were 4.30,
1.38 and 1.45 ms, excluding file loading; these are not controlled comparisons.
A separate 80x10 pseudo-terminal check with 20 generated plain paragraphs
prepended to the novel observed a preview scroll 33 ms after the pending
indicator. This checks responsiveness, not completion or peak memory.
After splitting indexing from layout, a further run showed the novel outline
18 ms after the pending indicator and a preview scroll 16 ms after input.
An exploratory comparison of independently rendered chunks also differed
from whole-document Glamour output, including for adjacent paragraphs and
for reference definitions, lists and fences crossing a split. Arbitrary
chunk boundaries are therefore not a verified correctness-preserving design.
For the novel alone, an exploratory heading-split renderer matched the
ANSI-stripped, right-trimmed lines after special-casing book-heading spacing.
That check did not establish equivalence for styling, links, references,
media, or boundaries inside other Markdown constructs.
Glamour buffers the document node and derives heading and paragraph spacing
from previous siblings. An AST-block prototype retained those sibling
relationships and matched visible text in a small mixed-Markdown example,
but produced different trailing padding. It has not been validated for
reader navigation, media, styles, or bounded concurrent rendering.
An additional per-block AST experiment matched exact ANSI output for 1,331
three-block combinations without HTML, but differed around HTML blocks.
It did not establish general equivalence or provide a windowed reader.
A subsequent conservative AST-chunk prototype rendered at most 128
paragraphs or headings per batch, but still retained every output line. On a
synthetic 6,000-heading/paragraph document at width 48, three uncontrolled
single-iteration samples took 1.282–1.366 s and allocated 342 MB, versus
1.328–1.372 s and 359 MB for the whole renderer. Mixed Markdown sometimes
failed its framing check and required a full-render fallback after partial
work. This modest allocation difference does not establish a bounded or
generally equivalent design, so the prototype was not retained.

An index of rendered section rows replaces the outline's per-view linear
scan. In isolated three-sample benchmarks, lookup at 382 sections had a
448 ns linear median versus 12.8 ns indexed; at 100,000 synthetic sections
the medians were 134 µs versus 28.5 ns. Both allocate zero per lookup; the
index itself adds one entry per mapped section and is built during rendering.
These uncontrolled microbenchmarks do not represent total frame latency.
After the change, novel scroll-and-view samples at 80x24 were 0.628–0.704 ms,
and outline-navigation samples at 120x40 were 5.10–5.19 ms per benchmark
operation. Small-document and image-heavy opening samples remained variable;
they do not establish an end-to-end speedup.

Editor entry now checks whether a document fits the native editor before
mapping the reader position back to source. For the oversized novel and a
synthetic visible line, three single-iteration samples of the previous
mapping-first sequence took 44–47 ms and allocated 14.9 MB; refusal-first
samples took 3.3–5.0 µs and allocated 40 bytes. This measures the model
operation, not terminal input latency.

An experimental streaming document-root layout was then integrated through a
local Glamour v1.0.0 fork. It flushes completed lines between top-level
blocks without splitting the parsed Markdown. Input with terminal controls or
malformed UTF-8 falls back to the original renderer. Three subsequent
`ReaderRenderCancellable` samples took 0.727–0.755 s and allocated 336 MB;
these are uncontrolled and not directly comparable with the earlier runs.
A fresh 80x24 pseudo-terminal queued-End run showed first content at 0.307 s,
End at 2.957 s, and sampled peak RSS of 166,800 KiB. It still renders and
retains all lines, and a single large Markdown block is still buffered.

The streaming cursor can now identify conservative restart points when its
root buffer and margin ANSI state are clear. In the novel at width 80 with
the dark theme, 11,386 of 11,768 top-level blocks yielded checkpoints; the
largest internal gap was three blocks. Sampled 32-block restarts matched the
original output byte-for-byte. This does not establish parity for arbitrary
styles or media and is not yet used by the reader. Three uncontrolled
single-iteration cursor-only runs, with the parsed AST reused and output
discarded, took 1.822–1.841 s without tracking and 1.864–1.908 s with
tracking, allocating about 557 MB in either case:

```sh
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelStreamingCheckpoints$' -benchmem -benchtime=1x -count=3
go test ./internal/tui -run '^TestNovelStreamingCheckpoints$' -count=1 -v
```

Reader output is now limited to 256 MiB of rendered bytes, 1,048,576 lines,
and 4 MiB per line, with an error instead of displaying a partial preview on
failure. Retained scaled image data is limited to 64 MiB and 4,096 distinct
image/width combinations. Long-document streaming also refuses a Markdown
block spanning more than 4 MiB of source; non-streamable reader input is
limited to 1 MiB. Embedded chart text or generated chart images are limited
to 64 MiB. Embedded sheets and charts are limited to 4,096 references total,
and expanded embedded Markdown is limited to 256 MiB. Image reads and
spreadsheet evaluation check for cancellation. These
limits do not fully bound Goldmark parsing, a block's
expanded internal buffers, spreadsheet evaluation state, or temporary image
decoding.
Three subsequent uncontrolled novel
`ReaderRenderCancellable` samples took 0.750–0.787 s and allocated 307 MB.
Small-document samples at 60x24, 80x24 and 120x40 took 0.98–3.14 ms; the
image-heavy document samples took 15.2–19.2 ms. They ran on the shared machine
and are not directly comparable to isolated latency distributions.

The reader now renders an explicitly provisional preview of up to 500 source
lines (at most 32 KiB) while indexing, then publishes verified output after a
full viewport and in roughly 500-source-line batches. The preview uses image
and chart placeholders; link styling may remain pending in later batches.
The scrolling viewport retains at most 1,024 lines, or enough for two very
tall terminal screens plus margins, while the complete rendered output is
still retained separately. The same bounded viewport is used after a large
reader finishes loading. The preview can change when later references are
resolved. A pseudo-terminal test at 80x24 observed the novel's `Title:` in the
initial full preview after 0.211, 0.323 and 0.326 seconds on the same shared
machine while tests ran concurrently. This marker is a workload-specific
proxy, not a general full-frame measurement. The model tests additionally
check that the preview contains at least a viewport's worth of rendered lines
before the index is delivered.

Consecutive scroll input is compacted during loading, including input ahead of
the rendered batches; it advances as each batch becomes available. A repeat
check on the same shared machine observed the first-viewport proxy at 0.280,
0.267 and 0.282 seconds. A queued-End run reached first content at 0.247 s
and End at 3.413 s; full-render time is not the first-viewport target.

On the novel at 80x24, three single-iteration samples of feeding the completed
render to the viewport took 67–76 ms for all lines and 0.68–0.72 ms for a
1,024-line window. This isolates viewport setup; the full render remains in
memory. Fresh pseudo-terminal first-viewport proxy samples were 0.211, 0.316
and 0.301 seconds while another terminal check ran concurrently.

Images in a long streaming document are now decoded when their Markdown block
is reached, rather than before the first verified batch. Chart and sheet
references are still expanded before streaming. Tests compare mixed nested and
linked image output with the whole-document renderer and check cancellation
before a later image is prepared.

On the shared machine, with race tests running concurrently, three further
pseudo-terminal first-viewport proxy samples were 0.341, 0.315 and 0.312
seconds. A separate queued-End run reached first content at 0.319 seconds
and End at 3.367 seconds, with 157.6 MiB sampled peak RSS. The full-viewport
proxy is specific to this fixture, and quitting immediately after it does not
measure completed-render memory. Three image-heavy reader samples took
32.8–53.7 ms at 60x24, 37.4–50.2 ms at 80x24 and 38.7–46.8 ms at 120x40;
these uncontrolled samples are not a reliable comparison with earlier runs.

Section positions are published with verified batches, so an outline or
section-key jump can complete once its target is rendered. After that change,
three first-viewport proxy samples were 0.298, 0.342 and 0.306 seconds; a
separate queued-End run reached first content at 0.310 seconds and End at
3.493 seconds, with 155.7 MiB sampled peak RSS. These shared-machine samples
were taken while other tests ran and do not establish a performance target.
Three further single-iteration model samples, while race tests were running,
measured a full 80x24 → 120x40 → 80x24 resize at 2.03–2.23 seconds and
about 600 MB of cumulative allocations; that benchmark renders synchronously
and is not an interactive latency measurement. Search completion took
34–46 ms and asynchronous search dispatch plus view took 0.47–0.57 ms.
Three outline jumps plus views took 2.7–3.2 ms with the completed render.

For a synthetic 1.1 MB document with an unsafe terminal control, a
conservative preflight now rejects certain oversized non-streamable sources
before parsing. Three isolated parse-then-refuse samples took 27–31 ms and
allocated 19.4 MB; the preflight path took 1.8–2.1 ms and allocated 78 KB.
Sources that may be changed by HTML or embedded references still use the later
check, so this does not bound or cancel parsing in general. The preflight now
also checks link rewriting and CRLF normalization when it is safe to do so.

```sh
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkReaderUnsafeFallback$' -benchmem -benchtime=1x -count=3
go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderViewport$' -benchmem -benchtime=1x -count=3
go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment /absolute/path/war-and-peace.md --full
```

After loading, eligible long documents, including embedded images and charts,
retain a checkpoint index and one rendered window. Distant windows
and rendered-text searches are reconstructed asynchronously; the index has
4,096 entries and an 8 MiB shallow accounting limit, with a 16 MiB source and
500,000 AST-visit eligibility cap. A cached link index is limited to 8 MiB;
documents exceeding it fall back to retaining the full render. The media-slot
index has an estimated 8 MiB limit. A sparse row index remains
proportional to the number of rendered lines. The full render is still retained
during loading,
and ineligible documents retain it afterward. Parsing and reflow still process
the complete document; the AST and temporary rendering allocations are not
bounded by the shallow checkpoint limit.

On the shared machine, after checkpoint integration, three 80x24 PTY
first-viewport proxy samples were 0.306, 0.313 and 0.317 seconds. A separate
queued-End/Home round trip reached first content at 0.333 seconds, End at
3.215 seconds and Home at 3.249 seconds, with 163 MiB sampled peak RSS.
These fixture-specific observations are not full-frame or physical-terminal
measurements, and do not establish targets. Single-iteration synchronous model
benchmarks measured novel rendering at 0.87–1.00 seconds across 60x24,
80x24 and 120x40; a two-way resize took 1.85 seconds. These measure full work,
not the time to the first useful screen. Single-iteration image-heavy document
samples were 25.6–39.1 ms across those sizes; the two-slide presentation
sample was 7.3 ms. These ran with other tests on the shared machine.
After media replay and experimental source-position indexing were added, three first-viewport
proxy samples were 0.226, 0.340 and 0.323 seconds. A queued-End/Home run
reached first content at 0.244 seconds, End at 3.560 seconds and Home at
3.594 seconds, with 167 MiB sampled peak RSS; race tests ran concurrently.
After the expanded-content and fallback safeguards, three further
first-viewport proxy samples were 0.371, 0.362 and 0.340 seconds.
Using `pyte` 0.8.2 to reconstruct the actual 80x24 terminal screen, three
samples reached a frame with both the Gutenberg opening and the title visible,
and at least 14 nonblank reader rows, in 0.325, 0.315 and 0.316 seconds.
This is still specific to the saved novel, and includes terminal-emulation
overhead; it is not a physical-terminal measurement.
After deferring the experimental position mapping, three equivalent samples
were 0.352, 0.321 and 0.357 seconds.
After raising the rendered-output limit to 256 MiB, a temporary 16 MiB
document made from five full copies of the saved novel body plus 303,217 bytes
of a sixth reached a terminal-emulated first screen in 0.533, 0.464 and
0.495 seconds at 80x24. A separate PTY run finished rendering and navigated
End/Home, with 690 MiB sampled peak process RSS under `GOMEMLIMIT=1GiB`.
These shared-machine observations are not physical-terminal measurements;
the first-screen harness used `GOMEMLIMIT=512MiB`.
Three single-iteration checkpoint reconstructions of 1,024 rows took
30–40 ms in the middle and 19–21 ms near the end. Before search indexing,
whole-document rendered search took 1.37–1.44 seconds and allocated about
314 MB cumulatively. A bounded 16 MiB plain-text search index reduced three
subsequent samples to 18–30 ms and about 240 KB allocated per search.
Documents whose indexes exceed that limit fall back to asynchronous rerendered
search. These are shared-machine samples, not peak memory or interactive latency.
After deferring position mapping, three further indexed-search samples were
13–15 ms.
After reusing the streamed preview's row index at completion, three PTY
first-viewport proxy samples were 0.320, 0.326 and 0.344 seconds; a separate
End/Home round trip reached first content at 0.300 seconds, End at 3.483
seconds and Home at 3.517 seconds, with 174 MiB sampled peak RSS. The proxy
does not validate the terminal-emulated screen, and these shared-machine
samples do not isolate the row-index optimization.
For the novel body with one added internal link, three link-rewrite benchmark
samples using the previous line-slice approach took 3.4–3.8 ms and allocated
about 5.4 MB per operation; line-by-line rewriting took 2.2–2.6 ms and
allocated about 3.3 MB. These are cumulative allocations, not peak memory,
and the benchmarks ran on the shared machine alongside focused tests.
After that change, terminal-emulated first-screen samples were 0.397, 0.433
and 0.349 seconds. A separate End/Home round trip reached first content at
0.349 seconds, End at 7.598 seconds and Home at 7.665 seconds, with 171 MiB
sampled peak RSS. These uncontrolled measurements are fixture-specific and
do not establish a change in full-render performance.
Single-iteration model checks after the link change measured 2.5–4.4 ms for
the small document, 35.9–44.1 ms for the image-heavy document, and 1.66–1.97
seconds for synchronous full novel rendering at 60x24, 80x24 and 120x40.
The novel figures are full-render timings, not first-screen latency, and are
not controlled comparisons against the earlier shared-machine samples.
For the novel with one added internal link and no reference definitions,
skipping a redundant Markdown parse reduced three single-iteration full-render
samples from 1.90–2.11 seconds and about 364 MB cumulative allocations to
1.82–1.84 seconds and about 337 MB. These are shared-machine full-render
samples, not first-screen timings or peak memory.
With an internal link added near the end of the novel's body, three PTY
first-viewport proxy samples were 0.325, 0.375 and 0.339 seconds. A separate
End/Home round trip succeeded. The proxy checks terminal output markers,
not the reconstructed screen, and uses a temporary copy of the saved fixture.

```sh
go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment /absolute/path/war-and-peace.md --roundtrip
go run internal/tui/testdata/reader-open-pty.go /absolute/path/parchment /absolute/path/war-and-peace.md --screen
go test internal/tui/testdata/reader-open-pty.go internal/tui/testdata/reader-open-pty_test.go
go test ./internal/tui -run '^$' -bench '^BenchmarkNovelDocumentScreen/(Reader(60x24|80x24|120x40)|ReaderSearch80x24|ReaderResize80x24To120x40)$' -benchmem -benchtime=1x -count=1
PARCHMENT_BENCH_MEDIA=1 go test ./internal/tui -run '^$' -bench '^BenchmarkEmbeddedMedia' -benchmem -benchtime=1x -count=1
go test ./internal/tui -run '^$' -bench '^BenchmarkNovelCheckpointWindows$' -benchmem -benchtime=1x -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/LinkRewrite(Split|Streaming)$' -benchmem -benchtime=100ms -count=3
GOMEMLIMIT=512MiB go test ./internal/tui -run '^$' -bench '^BenchmarkNovelReaderPipeline/ReaderRenderInlineLink$' -benchmem -benchtime=1x -count=3
```

The Linux PTY harness uses the repository's existing Go dependencies.
Its `--screen` mode reconstructs the screen using a small terminal emulator;
earlier screen measurements above used `pyte`.
