package tui

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/ramjac/parchment/internal/artifactfile"
	"github.com/ramjac/parchment/internal/document"
	"github.com/ramjac/parchment/internal/tui/editor"
)

// The document reader shows the saved document continuously, with an
// outline of its headings beside them on wide terminals. Existing documents
// open here; e switches to the editor and leaving the editor returns here.

const asyncReaderSearchBytes = 128 << 10
const maxReaderSearchMatches = 1 << 20
const maxQueuedReaderInput = 256
const maxReaderPreambleBlocks = 16
const maxReaderProgressBytes = 256 << 10
const readerProgressSourceLines = 500
const readerProgressBatchLines = 500
const maxEarlyReaderBytes = 32 << 10
const readerPreviewWindowLines = 1024

type readerQueuedTarget struct {
	slug       string
	occurrence int
	outline    bool
}
type readerQueuedWheel struct {
	event   mouseEvent
	outline bool
}

type readerRepeatedScroll struct {
	kind  string
	count int
}

func readerScrollKind(input tea.Msg) string {
	switch input := input.(type) {
	case tea.KeyPressMsg:
		switch input.String() {
		case "down", "j":
			return "down"
		case "up", "k":
			return "up"
		case "pgdown", "space":
			return "page down"
		case "pgup":
			return "page up"
		}
	case readerQueuedWheel:
		if !input.outline {
			switch input.event.Button {
			case mouseWheelDown:
				return "wheel down"
			case mouseWheelUp:
				return "wheel up"
			}
		}
	case readerRepeatedScroll:
		return input.kind
	}
	return ""
}

func (s *documentsScreen) readerScrollDelta(kind string) int {
	switch kind {
	case "down":
		return 1
	case "up":
		return -1
	case "page down":
		return s.reader.Height()
	case "page up":
		return -s.reader.Height()
	case "wheel down":
		if s.reader.MouseWheelEnabled {
			return s.reader.MouseWheelDelta
		}
	case "wheel up":
		if s.reader.MouseWheelEnabled {
			return -s.reader.MouseWheelDelta
		}
	}
	return 0
}

// compactReaderPreviewInputs combines scroll runs without changing input order.
// Pending input is only compacted before an event that could change key context.
func (s *documentsScreen) compactReaderPreviewInputs() {
	applied := s.readerPreviewInputs
	compacted := make([]tea.Msg, 0, len(s.readerQueuedInput))
	appendScroll := func(input tea.Msg, allowMerge bool) bool {
		kind := readerScrollKind(input)
		if !allowMerge || kind == "" {
			compacted = append(compacted, input)
			return false
		}
		count := 1
		if repeat, ok := input.(readerRepeatedScroll); ok {
			count = repeat.count
		}
		if n := len(compacted); n > 0 {
			if previous, ok := compacted[n-1].(readerRepeatedScroll); ok && previous.kind == kind {
				previous.count = min(maxReaderRenderedLines, previous.count+count)
				compacted[n-1] = previous
				return true
			}
		}
		compacted = append(compacted, readerRepeatedScroll{kind: kind, count: count})
		return true
	}
	for _, input := range s.readerQueuedInput[:applied] {
		appendScroll(input, true)
	}
	s.readerPreviewInputs = len(compacted)
	safe := !s.find.active
	for i, input := range s.readerQueuedInput[applied:] {
		// Never merge across the applied/pending boundary.
		if i == 0 && len(compacted) > 0 {
			kind := readerScrollKind(input)
			if kind != "" && safe {
				count := 1
				if repeat, ok := input.(readerRepeatedScroll); ok {
					count = repeat.count
				}
				compacted = append(compacted, readerRepeatedScroll{kind: kind, count: count})
				continue
			}
		}
		safe = appendScroll(input, safe)
	}
	s.readerQueuedInput = compacted
}

type readerSectionRow struct {
	row, section, highestSection int
}

func indexReaderSectionRows(ctx context.Context, rows []int) ([]readerSectionRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := make([]readerSectionRow, 0, len(rows))
	ordered := true
	for section, row := range rows {
		if section%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if row < 0 {
			continue
		}
		if len(index) > 0 && row < index[len(index)-1].row {
			ordered = false
		}
		index = append(index, readerSectionRow{row: row, section: section})
	}
	if !ordered {
		sort.Slice(index, func(i, j int) bool {
			if index[i].row != index[j].row {
				return index[i].row < index[j].row
			}
			return index[i].section < index[j].section
		})
	}
	highest := -1
	for i := range index {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		highest = max(highest, index[i].section)
		index[i].highestSection = highest
	}
	return index, nil
}

// startReading shows d, the saved document, in the reader.
func (s *documentsScreen) startReading(d document.Document) tea.Cmd {
	s.cancelReaderRender()
	s.stopDocumentAutosave()
	s.loadReaderDocument(d)
	s.body.Blur()
	s.reading = true
	s.readerOutlineManual = false
	s.readerClicks.reset()
	s.prompt, s.previewing, s.showChanges, s.reviewing = promptNone, false, false, false
	s.readerPreambleShown = false
	s.readerStreamPreview = false
	s.readerProvisional = false
	s.readerApproximate = false
	s.readerPreviewBase = 0
	s.readerWindowed = false
	if s.backgroundReader && len(d.Body) >= asyncReaderSearchBytes {
		s.readerLines, s.readerRows, s.readerLinks, s.outline = nil, nil, nil, nil
		s.readerRowIndex = nil
		s.readerSection = -1
		s.reader.SetContentLines(nil)
		s.showReaderPreamble()
	}
	return s.refreshReader()
}

// refreshReader reflows the saved document at the reader's current width.
func (s *documentsScreen) refreshReader() tea.Cmd {
	if s.backgroundReader && len(s.snapshot.Body) >= asyncReaderSearchBytes {
		return s.requestReaderRender()
	}
	s.renderReader()
	return nil
}

func (s *documentsScreen) renderReader() {
	searching, steps := s.readerSearching, s.readerSearchSteps
	s.cancelReaderSearch()
	offset, current := s.readerOffset(), s.readerMatch
	matchOffset := -1
	if current >= 0 && current < len(s.readerMatches) {
		row := s.readerMatches[current].row - offset
		if row >= 0 && row < s.reader.Height() {
			matchOffset = row
		}
	}
	section := s.currentSection()
	relative := 0
	if section >= 0 {
		relative = offset - s.readerRows[section]
	}
	width, height := s.width-4, s.height-5
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width = preview - 2
	}
	s.reader.SetWidth(max(width, 1))
	s.reader.SetHeight(max(height, 1))
	lines, outline, rows, links, err := s.renderReaderDocument(s.snapshot, s.reader.Width())
	if err != nil {
		s.errMessage = err.Error()
	} else {
		s.readerLines, s.outline, s.readerRows, s.readerLinks = lines, outline, rows, links
		s.readerRowIndex, _ = indexReaderSectionRows(context.Background(), rows)
	}
	if section >= 0 && section < len(rows) && rows[section] >= 0 {
		offset = rows[section] + relative
	}
	if s.find.visible && s.find.query() != "" {
		s.rebuildReaderMatches(s.find.query())
		if searching && len(s.readerMatches) > 0 {
			s.readerMatch = ((steps % len(s.readerMatches)) + len(s.readerMatches)) % len(s.readerMatches)
		} else {
			s.readerMatch = min(max(current, 0), len(s.readerMatches)-1)
		}
		if matchOffset >= 0 && s.readerMatch >= 0 {
			offset = s.readerMatches[s.readerMatch].row - matchOffset
		} else if searching && s.readerMatch >= 0 {
			offset = s.readerMatches[s.readerMatch].row
			s.readerSection = -1
			s.readerOutlineManual = false
		}
	}
	s.showReaderContent()
	s.setReaderOffset(offset)
}

func (s *documentsScreen) resizeReader() tea.Cmd {
	width := s.width - 4
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width = preview - 2
	}
	if s.reader.Width() != max(width, 1) || s.readerLines == nil {
		return s.refreshReader()
	}
	oldHeight, offset := s.reader.Height(), s.readerOffset()
	if s.readerCachePending {
		offset = s.readerCacheTarget
	}
	s.reader.SetHeight(max(s.height-5, 1))
	if s.readerRendering && s.readerPreambleShown && oldHeight != s.reader.Height() {
		s.replayReaderPreview()
	} else if s.readerWindowed && oldHeight != s.reader.Height() {
		s.setReaderOffset(offset)
	}
	return nil
}

func (s *documentsScreen) requestReaderRender() tea.Cmd {
	if !s.readerRendering && s.readerCachePending {
		s.readerReflowOffset = s.readerCacheTarget
		s.readerReflowTarget = true
	}
	s.cancelReaderWindowRequest()
	if !s.readerRendering {
		s.readerReflowQuery = ""
		if s.find.visible && s.find.query() != "" {
			s.readerReflowQuery = s.find.query()
			s.readerReflowMatch = s.readerMatch
			s.readerReflowPending = s.readerSearching
			s.readerReflowSteps = s.readerSearchSteps
			s.readerReflowRow = -1
			if s.readerMatch >= 0 && s.readerMatch < len(s.readerMatches) {
				row := s.readerMatches[s.readerMatch].row - s.readerOffset()
				if row >= 0 && row < s.reader.Height() {
					s.readerReflowRow = row
				}
			}
			s.cancelReaderSearch()
		}
	}
	s.readerRenderSession++
	s.readerRendering = true
	s.readerStreamPreview = false
	s.readerProvisional = false
	s.readerApproximate = false
	width := s.width - 4
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width = preview - 2
	}
	oldWidth, oldHeight := s.reader.Width(), s.reader.Height()
	s.reader.SetWidth(max(width, 1))
	s.reader.SetHeight(max(s.height-5, 1))
	if s.readerPreambleShown {
		if oldWidth != s.reader.Width() {
			s.showReaderPreamble()
		}
		if oldWidth != s.reader.Width() || oldHeight != s.reader.Height() {
			s.replayReaderPreview()
		}
		s.showReaderContent()
	}
	if s.readerRenderRunning {
		if s.readerRenderCancel != nil {
			s.readerRenderCancel()
		}
		return nil
	}
	return s.launchReaderRender()
}

// readerPreamble accepts complete, unambiguous plain-text blocks followed by
// a visible block. Blank lines prevent later Markdown from changing them.
func readerPreamble(body string) string {
	limit := min(len(body), 8194)
	end, accepted := 0, 0
	for blocks := 0; blocks < maxReaderPreambleBlocks && end < limit; blocks++ {
		next := strings.Index(body[end:limit], "\n\n")
		if next < 0 || end+next > 8192 {
			break
		}
		next += end
		if !readerPlainBlock(body[end:next]) || !readerVisibleBlockStart(body[next+2:]) {
			break
		}
		accepted = next
		end = next + 2
	}
	return body[:accepted]
}

func readerVisibleBlockStart(rest string) bool {
	if strings.HasPrefix(rest, "#") {
		i := 0
		for i < len(rest) && i < 6 && rest[i] == '#' {
			i++
		}
		if i >= len(rest) || rest[i] != ' ' {
			return false
		}
		rest = rest[i+1:]
	}
	return len(rest) > 0 && ((rest[0] >= 'a' && rest[0] <= 'z') || (rest[0] >= 'A' && rest[0] <= 'Z'))
}

func readerPlainBlock(block string) bool {
	if block == "" {
		return false
	}
	lines := strings.Split(block, "\n")
	if strings.HasPrefix(lines[0], "#") {
		if len(lines) != 1 {
			return false
		}
		heading := strings.TrimLeft(lines[0], "#")
		if count := len(lines[0]) - len(heading); count > 6 || !strings.HasPrefix(heading, " ") {
			return false
		}
		lines[0] = strings.TrimLeft(heading, " ")
	}
	for _, line := range lines {
		if line == "" || line[0] < 'A' || line[0] > 'Z' && (line[0] < 'a' || line[0] > 'z') {
			return false
		}
		for _, c := range line {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
				c >= '0' && c <= '9' || strings.ContainsRune(" .,;:!?\"'", c)) {
				return false
			}
		}
	}
	return true
}

func (s *documentsScreen) showReaderPreamble() {
	prefix := readerPreamble(s.snapshot.Body)
	if prefix == "" {
		return
	}
	width := s.width - 4
	if s.wideReader() {
		_, preview := s.readerPaneWidths()
		width = preview - 2
	}
	renderer := &documentsScreen{theme: s.theme}
	doc := document.Document{Body: prefix, Layout: s.snapshot.Layout}
	lines, _, _, _, err := renderer.renderReaderDocument(doc, max(width, 1))
	if err != nil {
		return
	}
	doc.Body += "\n\nPreamble"
	withFollowingBlock, _, _, _, err := renderer.renderReaderDocument(doc, max(width, 1))
	if err != nil || len(withFollowingBlock) < len(lines) {
		return
	}
	lines = withFollowingBlock[:len(lines)]
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return
	}
	s.readerLines = lines
	s.readerPreambleShown = true
	s.reader.SetWidth(max(width, 1))
	s.reader.SetHeight(max(s.height-5, 1))
	s.showReaderContent()
}

func (s *documentsScreen) launchReaderRender() tea.Cmd {
	s.readerRenderRunning = true
	var ctx context.Context
	if s.newOperationContext != nil {
		ctx, s.readerRenderCancel = s.newOperationContext()
	} else {
		ctx, s.readerRenderCancel = context.WithCancel(context.Background())
	}
	session, doc, width, theme := s.readerRenderSession, s.snapshot, s.reader.Width(), s.theme
	if s.backgroundReader {
		index := func() tea.Msg {
			if err := ctx.Err(); err != nil {
				return documentReaderIndexMsg{session: session, err: err}
			}
			indexedDoc := doc
			var err error
			indexedDoc.Body, err = artifactfile.StripPrivateBlocksContext(ctx, indexedDoc.Body)
			if err != nil {
				return documentReaderIndexMsg{session: session, err: err}
			}
			outline, err := document.ReaderOutlineContext(ctx, indexedDoc)
			if err != nil {
				return documentReaderIndexMsg{session: session, err: err}
			}
			rows := make([]int, len(outline))
			for i := range rows {
				if i%256 == 0 {
					if err := ctx.Err(); err != nil {
						return documentReaderIndexMsg{session: session, err: err}
					}
				}
				rows[i] = -1
			}
			return documentReaderIndexMsg{
				session: session, ctx: ctx, doc: indexedDoc, outline: outline, rows: rows,
				width: width, theme: theme, err: err,
			}
		}
		if len(doc.Body) < asyncReaderSearchBytes || len(s.readerLines) >= s.reader.Height() {
			return index
		}
		height := s.reader.Height()
		return func() tea.Msg {
			results := make(chan tea.Msg, 1)
			go func() { results <- index() }()
			body := earlyReaderPrefix(doc.Body)
			body = strings.ReplaceAll(body, "\r\n", "\n")
			var lines []string
			if body != "" && readerStreamSafe(ctx, []byte(body)) {
				preview := doc
				preview.Body = body
				renderer := &documentsScreen{theme: theme}
				renderer.imageState.earlyPreview = true
				rendered, _, _, _, err := renderer.renderReaderDocumentContext(ctx, preview, width)
				if err == nil && len(rendered) >= height {
					lines = rendered
				}
			}
			return documentReaderEarlyMsg{session: session, lines: lines, next: results}
		}
	}
	return func() tea.Msg {
		renderer := &documentsScreen{theme: theme}
		lines, outline, rows, links, err := renderer.renderReaderDocumentContext(ctx, doc, width)
		var rowIndex []readerSectionRow
		if err == nil {
			rowIndex, err = indexReaderSectionRows(ctx, rows)
		}
		return documentReaderRenderMsg{
			session: session, lines: lines, outline: outline, rows: rows,
			rowIndex: rowIndex, links: links, images: renderer.imageState, err: err,
		}
	}
}

func earlyReaderPrefix(body string) string {
	limit := min(len(body), maxEarlyReaderBytes)
	end := 0
	for lines := 0; lines < readerProgressSourceLines && end < limit; lines++ {
		next := strings.IndexByte(body[end:limit], '\n')
		if next < 0 {
			end = limit
			break
		}
		end += next + 1
	}
	for end > 0 && end < len(body) && !utf8.RuneStart(body[end]) {
		end--
	}
	return body[:end]
}

func (s *documentsScreen) readerEarlyFinished(msg documentReaderEarlyMsg) tea.Cmd {
	wait := func() tea.Msg { return <-msg.next }
	if !s.reading || !s.readerRendering {
		return nil
	}
	if msg.session != s.readerRenderSession || len(msg.lines) == 0 || s.readerStreamPreview ||
		(!s.readerPreambleShown && s.readerLines != nil) {
		return wait
	}
	s.readerLines = msg.lines
	s.readerPreambleShown = true
	s.readerProvisional = true
	s.showReaderContent()
	return wait
}

func (s *documentsScreen) readerIndexFinished(msg documentReaderIndexMsg) tea.Cmd {
	if msg.err != nil || msg.session != s.readerRenderSession || !s.reading || !s.readerRendering {
		return s.readerRenderFinished(documentReaderRenderMsg{session: msg.session, err: msg.err})
	}
	if s.readerPreambleShown || s.readerLines == nil {
		s.outline = msg.outline
		s.readerRows = msg.rows
		s.readerRowIndex = nil
		s.readerSection = -1
	}
	height := s.reader.Height()
	reusePreview := s.readerPreambleShown || s.readerLines == nil
	results := make(chan tea.Msg, 2)
	return func() tea.Msg {
		go func() {
			renderer := &documentsScreen{theme: msg.theme}
			published := 0
			renderer.readerProgress = func(lines []string) bool {
				if published == 0 && len(lines) < height {
					return false
				}
				for published < len(lines) && msg.ctx.Err() == nil {
					end, size := published, 0
					for end < len(lines) && end-published < readerProgressBatchLines {
						lineBytes := len(lines[end])
						if end > published && size+lineBytes > maxReaderProgressBytes {
							break
						}
						size += lineBytes
						end++
					}
					preview := make([]string, end-published)
					var sections []readerSectionRow
					for i, line := range lines[published:end] {
						preview[i] = strings.Map(func(r rune) rune {
							if section, ok := renderer.imageState.navigation[r]; ok {
								sections = append(sections, readerSectionRow{row: published + i, section: section})
								return -1
							}
							return r
						}, line)
					}
					sort.Slice(sections, func(i, j int) bool {
						if sections[i].row != sections[j].row {
							return sections[i].row < sections[j].row
						}
						return sections[i].section < sections[j].section
					})
					select {
					case results <- documentReaderPreviewMsg{
						session: msg.session, lines: preview, next: results,
						sections: sections,
						images: documentImageState{
							rows: renderer.imageState.pendingRows, graphicsUsed: renderer.imageState.graphicsUsed,
						},
						approximate: renderer.readerProgressApproximate,
					}:
						renderer.imageState.pendingRows = nil
						published = end
					case <-msg.ctx.Done():
						return false
					}
				}
				return published > 0
			}
			lines, outline, rows, links, err := renderer.renderReaderIndexedContext(msg.ctx, msg.doc, msg.width, msg.outline)
			var rowIndex []readerSectionRow
			if err == nil {
				rowIndex, err = indexReaderSectionRows(msg.ctx, rows)
			}
			var sparse []string
			if err == nil && renderer.readerSource != nil && len(lines) > readerPreviewWindowLines && !reusePreview {
				sparse = make([]string, len(lines))
			}
			result := documentReaderRenderMsg{
				session: msg.session, lines: lines, outline: outline, rows: rows,
				rowIndex: rowIndex, links: links, images: renderer.imageState, err: err,
				source: renderer.readerSource, sparse: sparse,
			}
			select {
			case results <- result:
			case <-msg.ctx.Done():
				select {
				case <-results:
				default:
				}
				results <- result
			}
		}()
		return <-results
	}
}

func (s *documentsScreen) readerPreviewFinished(msg documentReaderPreviewMsg) tea.Cmd {
	wait := func() tea.Msg { return <-msg.next }
	if !s.reading || !s.readerRendering {
		return nil
	}
	if msg.session != s.readerRenderSession {
		return wait
	}
	if !s.readerPreambleShown && s.readerLines != nil {
		return wait
	}
	first := !s.readerStreamPreview
	if first {
		s.readerLines = msg.lines
		s.readerStreamPreview = true
		s.imageState = documentImageState{rows: make(map[rune]documentImageRow), frame: s.imageState.frame, graphicsUsed: s.imageState.graphicsUsed}
	} else {
		s.readerLines = append(s.readerLines, msg.lines...)
	}
	highest := -1
	if n := len(s.readerRowIndex); n > 0 {
		highest = s.readerRowIndex[n-1].highestSection
	}
	for _, entry := range msg.sections {
		if entry.section < 0 || entry.section >= len(s.readerRows) {
			continue
		}
		s.readerRows[entry.section] = entry.row
		highest = max(highest, entry.section)
		entry.highestSection = highest
		s.readerRowIndex = append(s.readerRowIndex, entry)
	}
	s.readerProvisional = false
	s.readerApproximate = msg.approximate
	for marker, row := range msg.images.rows {
		s.imageState.rows[marker] = row
	}
	s.imageState.graphicsUsed = msg.images.graphicsUsed || s.imageState.graphicsUsed
	s.readerPreambleShown = true
	s.showReaderContent()
	if first {
		s.replayReaderPreview()
	}
	for !s.readerPreviewStale && s.readerPreviewInputs < len(s.readerQueuedInput) {
		index := s.readerPreviewInputs
		if repeat, ok := s.readerQueuedInput[index].(readerRepeatedScroll); ok {
			delta := s.readerScrollDelta(repeat.kind)
			if delta > 0 {
				old := s.readerPreviewBase + s.reader.YOffset()
				available := max(len(s.readerLines)+1-s.reader.Height()-old, 0) / delta
				if available == 0 {
					break
				}
				if available < repeat.count {
					if index > 0 {
						if previous, ok := s.readerQueuedInput[index-1].(readerRepeatedScroll); ok && previous.kind == repeat.kind {
							if !s.applyReaderPreviewScroll(readerRepeatedScroll{kind: repeat.kind, count: available}) {
								break
							}
							previous.count = min(maxReaderRenderedLines, previous.count+available)
							s.readerQueuedInput[index-1] = previous
							s.readerQueuedInput[index] = readerRepeatedScroll{kind: repeat.kind, count: repeat.count - available}
							break
						}
					}
					s.readerQueuedInput = append(s.readerQueuedInput, nil)
					copy(s.readerQueuedInput[index+2:], s.readerQueuedInput[index+1:])
					s.readerQueuedInput[index] = readerRepeatedScroll{kind: repeat.kind, count: available}
					s.readerQueuedInput[index+1] = readerRepeatedScroll{kind: repeat.kind, count: repeat.count - available}
				}
			}
		}
		if !s.applyReaderPreviewScroll(s.readerQueuedInput[index]) {
			break
		}
		s.readerPreviewInputs++
	}
	return wait
}

func (s *documentsScreen) cancelReaderRender() {
	s.cancelReaderWindow()
	s.readerRenderSession++
	if s.readerRenderCancel != nil {
		s.readerRenderCancel()
	}
	s.readerRendering = false
	s.readerQueuedInput = nil
	s.readerPreviewInputs = 0
	s.readerPreviewStale = false
	s.readerReflowQuery = ""
	s.readerReflowTarget = false
	s.readerPreambleShown = false
	s.readerStreamPreview = false
	s.readerProvisional = false
	s.readerApproximate = false
	s.readerPreviewBase = 0
	s.readerWindowed = false
}

func (s *documentsScreen) readerRenderFinished(msg documentReaderRenderMsg) tea.Cmd {
	s.readerRenderRunning = false
	if s.readerRenderCancel != nil {
		s.readerRenderCancel()
		s.readerRenderCancel = nil
	}
	if msg.session != s.readerRenderSession {
		if s.reading && s.readerRendering {
			return s.launchReaderRender()
		}
		return nil
	}
	if !s.reading || !s.readerRendering {
		return nil
	}
	s.readerRendering = false
	offset := s.readerOffset()
	section := s.currentSection()
	if s.readerReflowTarget {
		offset = s.readerReflowOffset
		if s.readerSection < 0 {
			section = s.sectionAtReaderRow(offset + 1)
		}
	}
	s.readerReflowTarget = false
	previewShown := s.readerPreambleShown
	var reusablePreview []string
	if s.readerStreamPreview && len(s.readerLines) == len(msg.lines) && len(msg.lines) > 0 &&
		&s.readerLines[0] != &msg.lines[0] {
		reusablePreview = s.readerLines
	}
	s.readerPreambleShown = false
	s.readerStreamPreview = false
	s.readerProvisional = false
	s.readerApproximate = false
	if msg.err != nil {
		s.errMessage = msg.err.Error()
		if previewShown {
			s.cancelReaderWindow()
			s.readerLines = nil
			s.reader.SetContentLines(nil)
			s.readerMatches, s.readerMatch = nil, -1
			s.readerRows = nil
			s.readerRowIndex = nil
			s.readerSection = -1
			s.readerPreviewBase = 0
			s.readerWindowed = false
		}
		s.readerQueuedInput = nil
		s.readerPreviewInputs = 0
		s.readerPreviewStale = false
		if !previewShown && s.readerCache != nil {
			s.setReaderOffset(offset)
		}
		return nil
	}
	s.readerPreviewBase = 0
	if s.readerPreviewInputs > 0 {
		offset = 0
	}
	relative := 0
	if section >= 0 {
		relative = offset - s.readerRows[section]
	}
	s.cancelReaderWindow()
	s.readerMatches, s.readerMatch = nil, -1
	s.readerLines, s.outline, s.readerRows, s.readerLinks = msg.lines, msg.outline, msg.rows, msg.links
	s.readerRowIndex = msg.rowIndex
	s.readerWindowed = s.backgroundReader && len(msg.lines) > readerPreviewWindowLines
	msg.images.frame = s.imageState.frame
	msg.images.graphicsUsed = msg.images.graphicsUsed || s.imageState.graphicsUsed
	s.imageState = msg.images
	if section >= 0 && section < len(msg.rows) && msg.rows[section] >= 0 {
		offset = msg.rows[section] + relative
	}
	s.showReaderContent()
	s.setReaderOffset(offset)
	var cmds []tea.Cmd
	if s.find.visible && s.find.query() != "" {
		cmds = append(cmds, s.searchReader(s.find.query()))
		if s.find.query() == s.readerReflowQuery {
			if s.readerReflowPending {
				s.readerSearchSteps = s.readerReflowSteps
			} else {
				s.readerSearchSteps = max(s.readerReflowMatch, 0)
			}
			if s.readerReflowRow >= 0 {
				s.readerSearchRestore, s.readerSearchRow = true, s.readerReflowRow
			}
		}
	}
	s.readerReflowQuery = ""
	queued := s.readerQueuedInput
	s.readerQueuedInput = nil
	if s.readerPreviewInputs > 0 {
		s.readerSection = -1
		s.readerOutlineManual = false
	}
	s.readerPreviewInputs = 0
	s.readerPreviewStale = false
	for _, input := range queued {
		if !s.reading {
			break
		}
		switch input := input.(type) {
		case readerRepeatedScroll:
			delta := s.readerScrollDelta(input.kind)
			if delta > 0 {
				s.setReaderOffset(s.readerOffset() + min(input.count, maxReaderRenderedLines/delta+1)*delta)
			} else if delta < 0 {
				s.setReaderOffset(s.readerOffset() - min(input.count, maxReaderRenderedLines/-delta+1)*-delta)
			}
			s.readerSection = -1
			s.readerOutlineManual = false
		case readerQueuedTarget:
			if input.outline {
				remaining := input.occurrence
				for index, section := range s.outline {
					if strings.EqualFold(section.Slug, input.slug) {
						if remaining == 0 {
							s.gotoSection(index)
							break
						}
						remaining--
					}
				}
			} else {
				s.navigateToTarget(input.slug)
			}
		case readerQueuedWheel:
			if input.outline {
				step := 3
				if input.event.Button == mouseWheelUp {
					step = -step
				}
				s.scrollReaderOutline(step)
			} else {
				s.scrollReader(toTeaMouseMessage(input.event))
			}
		default:
			cmds = append(cmds, s.updateDocument(input))
		}
	}
	if msg.source != nil && len(msg.lines) > readerPreviewWindowLines {
		sparse := msg.sparse
		if len(sparse) != len(msg.lines) {
			if len(reusablePreview) == len(msg.lines) {
				sparse = reusablePreview
				clear(sparse)
			} else {
				sparse = make([]string, len(msg.lines))
			}
		}
		offset := s.readerOffset()
		base := max(offset-256, 0)
		end := min(len(msg.lines), base+max(readerPreviewWindowLines, 2*s.reader.Height()+512))
		copy(sparse[base:end], msg.lines[base:end])
		s.readerLines = sparse
		s.readerCache = msg.source
		s.readerCacheBase, s.readerCacheEnd = base, end
		s.setReaderWindow(offset, offset, false)
	}
	return tea.Batch(cmds...)
}

func (s *documentsScreen) showReaderContent() {
	offset := s.readerOffset()
	loading := s.readerRendering && s.readerPreambleShown
	if loading || s.readerWindowed {
		s.setReaderWindow(offset, offset, loading)
		return
	}
	s.readerPreviewBase = 0
	s.reader.SetContentLines(s.readerLines)
	s.reader.SetYOffset(offset)
}

func (s *documentsScreen) setReaderPreviewWindow(offset, focus int) {
	s.setReaderWindow(offset, focus, true)
}

func (s *documentsScreen) readerOffset() int {
	return s.readerPreviewBase + s.reader.YOffset()
}

func (s *documentsScreen) setReaderOffset(offset int) {
	offset = min(max(offset, 0), max(len(s.readerLines)-s.reader.Height(), 0))
	if s.readerCache != nil {
		end := min(offset+s.reader.Height(), len(s.readerLines))
		if offset < s.readerCacheBase || end > s.readerCacheEnd {
			s.readerCacheSession++
			if s.readerCacheCancel != nil {
				s.readerCacheCancel()
				s.readerCacheCancel = nil
			}
			s.readerCacheTarget = offset
			s.readerCachePending = true
			s.readerCacheDispatch = true
			return
		}
		if s.readerCachePending {
			s.readerCacheSession++
			if s.readerCacheCancel != nil {
				s.readerCacheCancel()
				s.readerCacheCancel = nil
			}
			s.readerCachePending = false
			s.readerCacheDispatch = false
		}
	}
	if s.readerWindowed {
		end := s.readerPreviewBase + s.reader.TotalLineCount()
		if offset < s.readerPreviewBase || offset+s.reader.Height() > end {
			s.setReaderWindow(offset, offset, false)
			return
		}
		offset -= s.readerPreviewBase
	}
	s.reader.SetYOffset(offset)
}

func (s *documentsScreen) cancelReaderWindow() {
	s.cancelReaderWindowRequest()
	s.readerCache = nil
	s.readerCacheBase, s.readerCacheEnd = 0, 0
}

func (s *documentsScreen) cancelReaderWindowRequest() {
	s.readerCacheSession++
	if s.readerCacheCancel != nil {
		s.readerCacheCancel()
		s.readerCacheCancel = nil
	}
	s.readerCachePending = false
	s.readerCacheDispatch = false
}

func (s *documentsScreen) dispatchReaderWindow() tea.Cmd {
	if !s.readerCacheDispatch || s.readerCache == nil || !s.reading {
		return nil
	}
	s.readerCacheDispatch = false
	var ctx context.Context
	if s.newOperationContext != nil {
		ctx, s.readerCacheCancel = s.newOperationContext()
	} else {
		ctx, s.readerCacheCancel = context.WithCancel(context.Background())
	}
	session, offset, source := s.readerCacheSession, s.readerCacheTarget, s.readerCache
	base := max(offset-256, 0)
	end := min(len(s.readerLines), base+max(readerPreviewWindowLines, 2*s.reader.Height()+512))
	return func() tea.Msg {
		lines, err := source.lines(ctx, base, end)
		return documentReaderWindowMsg{session: session, offset: offset, base: base, end: end, lines: lines, err: err}
	}
}

func (s *documentsScreen) readerWindowFinished(msg documentReaderWindowMsg) {
	if !s.reading || s.readerCache == nil || !s.readerCachePending || msg.session != s.readerCacheSession {
		return
	}
	if s.readerCacheCancel != nil {
		s.readerCacheCancel()
		s.readerCacheCancel = nil
	}
	s.readerCachePending = false
	if msg.err == nil && len(msg.lines) != msg.end-msg.base {
		msg.err = errors.New("incomplete rendered window")
	}
	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			s.errMessage = "Reader window: " + msg.err.Error()
		}
		return
	}
	clear(s.readerLines[s.readerCacheBase:s.readerCacheEnd])
	copy(s.readerLines[msg.base:], msg.lines)
	s.readerCacheBase, s.readerCacheEnd = msg.base, msg.base+len(msg.lines)
	s.setReaderWindow(msg.offset, msg.offset, false)
}

func (s *documentsScreen) setReaderWindow(offset, focus int, loading bool) {
	total := len(s.readerLines)
	if loading {
		total++
	}
	offset = min(max(offset, 0), max(total-s.reader.Height(), 0))
	base := max(0, min(offset, focus)-256)
	size := max(readerPreviewWindowLines, 2*s.reader.Height()+512)
	end := min(len(s.readerLines), base+size)
	if !loading && s.readerCache != nil {
		base = max(base, s.readerCacheBase)
		end = min(end, s.readerCacheEnd)
	}
	content := append([]string(nil), s.readerLines[base:end]...)
	if loading && end == len(s.readerLines) {
		indicator := "Loading…"
		if s.readerProvisional {
			indicator = "Loading… preview may change"
		} else if s.readerApproximate {
			indicator = "Loading… link styling pending"
		}
		content = append(content, indicator)
	}
	s.readerPreviewBase = base
	s.reader.SetContentLines(content)
	s.reader.SetYOffset(offset - base)
}

func (s *documentsScreen) cancelReaderSearch() {
	s.readerSearchSession++
	if s.readerSearchCancel != nil {
		s.readerSearchCancel()
		s.readerSearchCancel = nil
	}
	s.readerSearching = false
	s.readerSearchSteps = 0
	s.readerSearchRestore = false
}

func (s *documentsScreen) searchReader(query string) tea.Cmd {
	s.cancelReaderSearch()
	s.readerSearchError = ""
	s.readerMatches = nil
	s.readerMatch = -1
	if query == "" {
		return nil
	}
	if len(s.snapshot.Body) >= asyncReaderSearchBytes {
		var ctx context.Context
		var cancel context.CancelFunc
		if s.newOperationContext != nil {
			ctx, cancel = s.newOperationContext()
		} else {
			ctx, cancel = context.WithCancel(context.Background())
		}
		s.readerSearchCancel = cancel
		s.readerSearching = true
		session, lines, source := s.readerSearchSession, s.readerLines, s.readerCache
		return func() tea.Msg {
			if source != nil {
				matches, err := source.matches(ctx, query, len(lines), maxReaderSearchMatches)
				return documentReaderSearchMsg{session: session, query: query, matches: matches, err: err}
			}
			matches, err := renderedReaderMatchesContext(ctx, lines, query, maxReaderSearchMatches)
			return documentReaderSearchMsg{session: session, query: query, matches: matches, err: err}
		}
	}
	s.rebuildReaderMatches(query)
	if len(s.readerMatches) > 0 {
		s.readerMatch = 0
		s.gotoReaderMatch()
	}
	return nil
}

func (s *documentsScreen) readerSearchFinished(msg documentReaderSearchMsg) {
	if msg.session != s.readerSearchSession || !s.reading || !s.find.visible || msg.query != s.find.query() {
		return
	}
	steps := s.readerSearchSteps
	restore, row := s.readerSearchRestore, s.readerSearchRow
	s.cancelReaderSearch()
	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			s.readerSearchError = "Search failed: " + msg.err.Error()
		}
		return
	}
	s.readerMatches = msg.matches
	s.readerMatch = -1
	if len(s.readerMatches) > 0 {
		s.readerMatch = ((steps % len(s.readerMatches)) + len(s.readerMatches)) % len(s.readerMatches)
		s.gotoReaderMatch()
		if restore {
			s.setReaderOffset(s.readerMatches[s.readerMatch].row - row)
		}
	}
}

func (s *documentsScreen) rebuildReaderMatches(query string) {
	s.readerMatches = nil
	s.readerMatches = renderedMatches(s.readerLines, query, 0)
	s.readerMatch = -1
}

func (s *documentsScreen) gotoReaderMatch() {
	if s.readerMatch < 0 || s.readerMatch >= len(s.readerMatches) {
		return
	}
	match := s.readerMatches[s.readerMatch]
	s.setReaderOffset(match.row)
	s.readerSection = -1
	s.readerOutlineManual = false
}

func (s *documentsScreen) moveReaderMatch(step int) {
	if s.readerSearching {
		s.readerSearchSteps += step
		s.readerSearchRestore = false
		return
	}
	if len(s.readerMatches) == 0 {
		return
	}
	s.readerMatch = (s.readerMatch + step) % len(s.readerMatches)
	if s.readerMatch < 0 {
		s.readerMatch += len(s.readerMatches)
	}
	s.gotoReaderMatch()
}

func (s *documentsScreen) wideReader() bool { return s.width >= 80 }

func (s *documentsScreen) readerPaneWidths() (outline, preview int) {
	outline = max(s.width/3, 24)
	return outline, max(s.width-outline-4, 30)
}

func (s *documentsScreen) scrollReader(msg tea.Msg) {
	if s.readerWindowed && !s.readerPreambleShown {
		kind := readerScrollKind(msg)
		if wheel, ok := msg.(tea.MouseWheelMsg); ok {
			switch wheel.Button {
			case tea.MouseWheelDown:
				kind = "wheel down"
			case tea.MouseWheelUp:
				kind = "wheel up"
			}
		}
		if kind != "" {
			offset := s.readerOffset()
			if s.readerCachePending {
				offset = s.readerCacheTarget
			}
			s.setReaderOffset(offset + s.readerScrollDelta(kind))
			s.readerSection = -1
			s.readerOutlineManual = false
			return
		}
	}
	s.reader, _ = s.reader.Update(msg)
	s.readerSection = -1
	s.readerOutlineManual = false
}

// applyReaderPreviewScroll only advances when the requested movement is
// inside the verified preview. Other input remains queued for the render.
func (s *documentsScreen) applyReaderPreviewScroll(input tea.Msg) bool {
	if target, ok := input.(readerQueuedTarget); ok {
		remaining := target.occurrence
		for index, section := range s.outline {
			if !strings.EqualFold(section.Slug, target.slug) {
				continue
			}
			if target.outline && remaining > 0 {
				remaining--
				continue
			}
			if index >= len(s.readerRows) || s.readerRows[index] < 0 ||
				s.readerRows[index] > max(len(s.readerLines)+1-s.reader.Height(), 0) {
				return false
			}
			s.setReaderPreviewWindow(s.readerRows[index], s.readerRows[index])
			s.readerSection = index
			s.readerOutlineManual = false
			return true
		}
		return false
	}
	if repeat, ok := input.(readerRepeatedScroll); ok {
		delta := s.readerScrollDelta(repeat.kind)
		old := s.readerPreviewBase + s.reader.YOffset()
		target := old
		if delta > 0 {
			available := max(len(s.readerLines)+1-s.reader.Height(), 0) - old
			if available < 0 || repeat.count > available/delta {
				return false
			}
			target += repeat.count * delta
		} else if delta < 0 {
			if repeat.count >= (old-1)/-delta+1 {
				target = 0
			} else {
				target += repeat.count * delta
			}
		}
		s.setReaderPreviewWindow(target, target)
		s.readerSection = -1
		s.readerOutlineManual = false
		return true
	}
	var msg tea.Msg
	delta := 0
	switch input := input.(type) {
	case tea.KeyPressMsg:
		msg = input
		switch input.String() {
		case "down", "j":
			delta = 1
		case "pgdown", "space":
			delta = s.reader.Height()
		case "up", "k":
			delta = -1
		case "pgup":
			delta = -s.reader.Height()
		case "home", "g":
			s.setReaderPreviewWindow(0, 0)
			s.reader.GotoTop()
			s.readerSection = -1
			s.readerOutlineManual = false
			return true
		case "{", "}":
			step := -1
			if input.String() == "}" {
				step = 1
			}
			index := s.currentSection() + step
			if index < 0 || index >= len(s.outline) {
				return true
			}
			if index >= len(s.readerRows) || s.readerRows[index] < 0 ||
				s.readerRows[index] > max(len(s.readerLines)+1-s.reader.Height(), 0) {
				return false
			}
			s.setReaderPreviewWindow(s.readerRows[index], s.readerRows[index])
			s.readerSection = index
			s.readerOutlineManual = false
			return true
		default:
			return false
		}
	case readerQueuedWheel:
		if input.outline {
			return false
		}
		msg = toTeaMouseMessage(input.event)
		switch input.event.Button {
		case mouseWheelDown:
			delta = s.reader.MouseWheelDelta
		case mouseWheelUp:
			delta = -s.reader.MouseWheelDelta
		default:
			return false
		}
	default:
		return false
	}
	old := s.readerPreviewBase + s.reader.YOffset()
	if delta > 0 && old+delta > max(len(s.readerLines)+1-s.reader.Height(), 0) {
		return false
	}
	s.setReaderPreviewWindow(old, old+delta)
	next, _ := s.reader.Update(msg)
	s.reader = next
	s.readerSection = -1
	s.readerOutlineManual = false
	return true
}

func (s *documentsScreen) queueReaderInput(input tea.Msg) {
	if len(s.readerQueuedInput) >= maxQueuedReaderInput {
		s.compactReaderPreviewInputs()
	}
	if len(s.readerQueuedInput) >= maxQueuedReaderInput {
		s.errMessage = "Reader is still rendering; input queue is full"
		return
	}
	if s.readerPreambleShown && !s.readerProvisional && !s.readerPreviewStale && !s.find.active &&
		s.readerPreviewInputs == len(s.readerQueuedInput) && s.applyReaderPreviewScroll(input) {
		s.readerPreviewInputs++
	}
	s.readerQueuedInput = append(s.readerQueuedInput, input)
}

func (s *documentsScreen) replayReaderPreview() {
	s.setReaderPreviewWindow(0, 0)
	s.readerSection = -1
	s.readerPreviewStale = false
	for _, input := range s.readerQueuedInput[:s.readerPreviewInputs] {
		if !s.applyReaderPreviewScroll(input) {
			s.readerPreviewStale = true
			break
		}
	}
}

func (s *documentsScreen) readerPendingNavigationOnly() bool {
	for _, input := range s.readerQueuedInput[s.readerPreviewInputs:] {
		if readerScrollKind(input) != "" {
			continue
		}
		switch input := input.(type) {
		case readerQueuedTarget, readerQueuedWheel:
		case tea.KeyPressMsg:
			switch input.String() {
			case "home", "g", "end", "G", "{", "}":
			default:
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (s *documentsScreen) updateReaderKey(msg tea.KeyPressMsg) tea.Cmd {
	s.readerClicks.reset()
	key := msg.String()
	immediate := key == "ctrl+c" || (!s.find.active &&
		(((key == "q" || key == "esc") && s.readerPendingNavigationOnly()) ||
			(len(s.readerQueuedInput) == 0 && key == "e" && !s.readerProvisional)))
	if s.readerRendering && !immediate {
		s.queueReaderInput(msg)
		return nil
	}
	if s.prompt != promptNone {
		return s.updatePrompt(msg)
	}
	if s.find.active {
		return s.updateReaderFindKey(msg)
	}
	if s.find.visible {
		switch key {
		case "n":
			s.moveReaderMatch(1)
			return nil
		case "N":
			s.moveReaderMatch(-1)
			return nil
		case "esc":
			s.find.close()
			return s.searchReader("")
		}
	}
	if key == "/" || key == "ctrl+f" {
		search := s.searchReader(s.find.query())
		return tea.Batch(search, s.find.open(s.width))
	}
	switch key {
	case "q", "esc", "ctrl+c":
		if s.showChanges {
			return s.updateChangesKey(msg)
		}
		return s.close()
	case "e":
		offset := s.readerOffset()
		cmd, ok := s.startEdit(s.snapshot)
		if !ok {
			return nil
		}
		line, col, row := 0, 0, 0
		if offset > 0 {
			preferredLine := sourceLineForSection(s.snapshot.Body, s.outline, s.sectionAtReaderRow(offset))
			if mappedLine, mappedCol, mappedRow, found := sourcePositionAtViewport(
				s.readerLines, offset, s.reader.Height(), s.snapshot.Body, preferredLine,
			); found {
				line, col, row = mappedLine, mappedCol, mappedRow
			} else {
				line = preferredLine
			}
		}
		s.body.SetCursorAtRow(editor.Point{Line: line, Col: col}, row)
		s.reading = false
		return cmd
	case "ctrl+p":
		return s.openPDFPrompt()
	case "v", "f4":
		return s.loadChanges()
	case "u", "ctrl+z":
		return s.history(true)
	case "ctrl+r":
		return s.history(false)
	case "ctrl+o":
		s.outlineVisible = !s.outlineVisible
	case "{":
		s.stepSection(-1)
	case "}":
		s.stepSection(1)
	case "down", "j", "pgdown", "space":
		s.scrollReader(msg)
	case "up", "k", "pgup":
		s.scrollReader(msg)
	case "home", "g":
		s.setReaderOffset(0)
		s.readerSection = -1
		s.readerOutlineManual = false
	case "end", "G":
		s.setReaderOffset(len(s.readerLines) - s.reader.Height())
		s.readerSection = -1
		s.readerOutlineManual = false
	}
	return nil
}

func (s *documentsScreen) updateReaderMouse(msg mouseEvent) tea.Cmd {
	if s.prompt != promptNone || msg.Action != mousePress {
		return nil
	}
	if s.readerRendering {
		var queued tea.Msg
		switch msg.Button {
		case mouseWheelDown, mouseWheelUp:
			outline := s.mouseInReaderOutline(msg)
			if outline && len(s.outline) > 0 && len(s.readerQueuedInput) == 0 {
				step := 3
				if msg.Button == mouseWheelUp {
					step = -step
				}
				s.scrollReaderOutline(step)
				return nil
			}
			queued = readerQueuedWheel{event: msg, outline: outline}
		case mouseLeft:
			if s.wideReader() {
				outlineWidth, _ := s.readerPaneWidths()
				if msg.X >= 1 && msg.X < outlineWidth+1 {
					_, targets := s.outlineLines()
					if row := msg.Y - 3; row >= 0 && row < len(targets) && targets[row] >= 0 {
						index := targets[row]
						slug := s.outline[index].Slug
						occurrence := 0
						for _, section := range s.outline[:index] {
							if strings.EqualFold(section.Slug, slug) {
								occurrence++
							}
						}
						queued = readerQueuedTarget{slug: slug, occurrence: occurrence, outline: true}
					}
				}
			}
			if queued == nil {
				row := msg.Y - 3 + s.readerPreviewBase + s.reader.YOffset()
				col := msg.X - 2
				if s.wideReader() {
					outlineWidth, _ := s.readerPaneWidths()
					col = msg.X - (outlineWidth + 1)
				}
				if msg.Y >= 3 && msg.Y < 3+s.reader.Height() && col >= 0 && col < s.reader.Width() {
					for _, box := range s.readerLinks {
						if box.row == row && col >= box.startCol && col < box.endCol {
							queued = readerQueuedTarget{slug: box.target}
							break
						}
					}
				}
			}
			if queued == nil {
				s.errMessage = "Wait for rendering before opening the editor from a click"
			}
		}
		if queued != nil {
			s.queueReaderInput(queued)
		}
		return nil
	}
	switch msg.Button {
	case mouseLeft:
		if s.wideReader() {
			outlineWidth, _ := s.readerPaneWidths()
			if msg.X >= 1 && msg.X < outlineWidth+1 {
				s.clickOutline(msg.X, msg.Y)
				s.readerClicks.reset()
				return nil
			}
		}
		if s.clickReaderLink(msg.X, msg.Y) {
			s.readerClicks.reset()
			return nil
		}
		if msg.Y < 3 || msg.Y >= 3+s.reader.Height() {
			s.readerClicks.reset()
			return nil
		}
		pageRow := msg.Y - 3 + s.readerOffset()
		pageCol := msg.X - 2
		if s.wideReader() {
			outlineWidth, _ := s.readerPaneWidths()
			pageCol = msg.X - (outlineWidth + 1)
		}
		if pageRow < 0 || pageRow >= len(s.readerLines) || pageCol < 0 || pageCol >= s.reader.Width() {
			s.readerClicks.reset()
			return nil
		}
		clickColumn := pageCol
		if _, start, ok := renderedWordHit(s.readerLines[pageRow], pageCol); ok {
			clickColumn = start
		}
		if s.readerClicks.click(clickColumn, pageRow, time.Now()) {
			return s.editAtReaderLocation(pageRow, pageCol)
		}
	case mouseWheelDown:
		if s.mouseInReaderOutline(msg) {
			s.scrollReaderOutline(3)
		} else {
			s.scrollReader(toTeaMouseMessage(msg))
		}
	case mouseWheelUp:
		if s.mouseInReaderOutline(msg) {
			s.scrollReaderOutline(-3)
		} else {
			s.scrollReader(toTeaMouseMessage(msg))
		}
	}
	return nil
}

func (s *documentsScreen) mouseInReaderOutline(msg mouseEvent) bool {
	if !s.wideReader() {
		return false
	}
	width, _ := s.readerPaneWidths()
	return msg.X >= 1 && msg.X < width+1 && msg.Y >= 3 && msg.Y < 3+s.reader.Height()
}

func (s *documentsScreen) readerOutlineStart() int {
	room := max(s.height-6, 1)
	if !s.readerOutlineManual {
		return visibleWindowStart(len(s.outline), max(s.currentSection(), 0), room)
	}
	return min(max(s.readerOutlineOffset, 0), max(len(s.outline)-room, 0))
}

func (s *documentsScreen) scrollReaderOutline(step int) {
	room := max(s.height-6, 1)
	s.readerOutlineOffset = min(max(s.readerOutlineStart()+step, 0), max(len(s.outline)-room, 0))
	s.readerOutlineManual = true
}

func (s *documentsScreen) clickReaderLink(x, y int) bool {
	if y < 3 || y >= 3+s.reader.Height() {
		return false
	}
	pageRow := (y - 3) + s.readerOffset()
	pageCol := x - 2
	if s.wideReader() {
		outlineWidth, _ := s.readerPaneWidths()
		pageCol = x - (outlineWidth + 1)
	}
	if pageCol < 0 || pageCol >= s.reader.Width() {
		return false
	}
	for _, box := range s.readerLinks {
		if box.row == pageRow && pageCol >= box.startCol && pageCol < box.endCol {
			s.navigateToTarget(box.target)
			return true
		}
	}
	return false
}

func (s *documentsScreen) editAtReaderLocation(row, column int) tea.Cmd {
	viewportRow := row - s.readerOffset()
	cmd, ok := s.startEdit(s.snapshot)
	if !ok {
		return nil
	}
	preferredLine := sourceLineForSection(s.snapshot.Body, s.outline, s.sectionAtReaderRow(row))
	line, col, found := sourcePositionForRenderedWord(s.readerLines[row], column, s.snapshot.Body, preferredLine)
	if !found {
		line, col = preferredLine, 0
	}
	s.body.SetCursorAtRow(editor.Point{Line: line, Col: col}, viewportRow)
	s.reading = false
	return cmd
}

func (s *documentsScreen) sectionAtReaderRow(row int) int {
	i := sort.Search(len(s.readerRowIndex), func(i int) bool {
		return s.readerRowIndex[i].row > row
	})
	if i == 0 {
		return -1
	}
	return s.readerRowIndex[i-1].section
}

func (s *documentsScreen) navigateToTarget(target string) {
	clean := strings.TrimPrefix(target, "#")
	if clean == "" {
		return
	}
	for i, sec := range s.outline {
		if strings.EqualFold(sec.Slug, clean) {
			s.gotoSection(i)
			return
		}
	}
}

// currentSection is the last section at or above the top of the viewport.
func (s *documentsScreen) currentSection() int {
	if s.readerSection >= 0 && s.readerSection < len(s.readerRows) {
		return s.readerSection
	}
	i := sort.Search(len(s.readerRowIndex), func(i int) bool {
		return s.readerRowIndex[i].row > s.readerOffset()+1
	})
	if i == 0 {
		return -1
	}
	return s.readerRowIndex[i-1].highestSection
}

func (s *documentsScreen) stepSection(step int) {
	next := s.currentSection() + step
	if next >= 0 && next < len(s.outline) {
		s.gotoSection(next)
	}
}

func (s *documentsScreen) gotoSection(index int) {
	if index >= 0 && index < len(s.readerRows) && s.readerRows[index] >= 0 {
		s.setReaderOffset(s.readerRows[index])
		s.readerSection = index
		s.readerOutlineManual = false
	}
}

// outlineLines renders the outline pane. targets maps each line to an
// outline index, or -1 when the line is not a section.
func (s *documentsScreen) outlineLines() (lines []string, targets []int) {
	add := func(line string, target int) {
		lines = append(lines, line)
		targets = append(targets, target)
	}
	add("Outline", -1)
	if len(s.outline) == 0 {
		add("  No headings", -1)
		return lines, targets
	}
	room := max(s.height-5-len(lines), 1)
	current := s.currentSection()
	start := s.readerOutlineStart()
	for i := start; i < min(start+room, len(s.outline)); i++ {
		section := s.outline[i]
		marker := "  "
		if i == current {
			marker = "› "
		}
		indent := strings.Repeat("  ", min(max(section.Level-1, 0), 4))
		add(marker+indent+sanitizeTerminalLine(section.Title), i)
	}
	return lines, targets
}

// clickOutline jumps to the section under a click in the outline pane. Rows
// start below the header line and the pane's top border.
func (s *documentsScreen) clickOutline(x, y int) {
	if !s.wideReader() {
		return
	}
	outlineWidth, _ := s.readerPaneWidths()
	if x < 1 || x >= outlineWidth+1 {
		return
	}
	_, targets := s.outlineLines()
	if row := y - 3; row >= 0 && row < len(targets) && targets[row] >= 0 {
		s.gotoSection(targets[row])
	}
}

func (s *documentsScreen) readerView(header string) string {
	shortcuts := "e edit  Ctrl+P PDF  ↑/↓ scroll  { } section  v changes  u undo  Ctrl+R redo  esc/q quit"
	if s.prompt != promptNone {
		shortcuts = "Enter export PDF  ·  Esc cancel"
		if s.prompt == promptPDFOverwrite {
			shortcuts = "y overwrite  ·  n/Esc cancel"
		}
	}
	readerContent := s.readerContentView()
	if s.prompt != promptNone {
		readerContent = s.promptInput.View() + "\n" + readerContent
	}
	if !s.wideReader() {
		shortcuts = runewidth.Truncate(shortcuts, max(s.width, 1), "…")
		return chromeLayout(s.width, s.height, header, shortcuts, readerContent, s.readerStatus(), s.theme.BorderColor)
	}
	outlineWidth, previewWidth := s.readerPaneWidths()
	lines, _ := s.outlineLines()
	for i, line := range lines {
		lines[i] = runewidth.Truncate(line, max(outlineWidth-2, 1), "…")
	}

	left := lipgloss.NewStyle().
		Width(outlineWidth-2).
		Border(lipgloss.NormalBorder(), false, true, false, false).
		BorderForeground(s.theme.BorderColor).
		PaddingRight(1).
		Render(strings.Join(lines, "\n"))

	right := lipgloss.NewStyle().
		Width(previewWidth - 2).
		PaddingLeft(1).
		Render(readerContent)

	content := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	if s.prompt == promptNone {
		shortcuts = "click outline to jump  " + shortcuts
	}
	shortcuts = runewidth.Truncate(shortcuts, max(s.width, 1), "…")
	return chromeLayout(s.width, s.height, header, shortcuts, content, s.readerStatus(), s.theme.BorderColor)
}

func (s *documentsScreen) readerContentView() string {
	query := s.find.query()
	if !s.find.visible || query == "" {
		return s.reader.View()
	}
	start := min(s.readerOffset(), len(s.readerLines))
	end := min(start+s.reader.Height(), len(s.readerLines))
	lines := highlightLines(s.readerLines[start:end], query, s.theme)
	reader := s.reader
	reader.SetContentLines(lines)
	reader.SetYOffset(0)
	return reader.View()
}

func (s *documentsScreen) readerStatus() string {
	if s.readerRendering {
		if s.errMessage != "" {
			return sanitizeTerminalLine(s.errMessage)
		}
		if s.readerProvisional {
			return "Loading… preview may change"
		}
		if s.readerApproximate {
			return "Loading… link styling pending"
		}
		return "Rendering document… some navigation pending"
	}
	if s.errMessage != "" {
		return s.statusLine()
	}
	if s.readerCachePending {
		return "Loading section…"
	}
	if s.find.visible {
		if s.readerSearchError != "" {
			s.find.input.SetWidth(max(s.width-14, 1))
			return ansi.Truncate(s.readerSearchError+"  "+s.find.input.View(), max(s.width, 1), "…")
		}
		if s.readerSearching {
			s.find.input.SetWidth(max(s.width-14, 1))
			return ansi.Truncate("Searching…  "+s.find.input.View(), max(s.width, 1), "…")
		}
		return s.find.view(len(s.readerMatches), s.readerMatch, s.width)
	}
	return s.statusLine()
}

func (s *documentsScreen) updateReaderFindKey(msg tea.Msg) tea.Cmd {
	action, cmd := s.find.update(msg)
	switch action {
	case findChanged:
		return tea.Batch(cmd, s.searchReader(s.find.query()))
	case findNext:
		s.moveReaderMatch(1)
		s.find.blur()
	case findPrevious:
		s.moveReaderMatch(-1)
		s.find.blur()
	case findClosed:
		return tea.Batch(cmd, s.searchReader(""))
	}
	return cmd
}

// leaveEditor returns from the editor to the reader, discarding unsaved edits
// and the autosaved draft, as closing the editor normally does.
func (s *documentsScreen) leaveEditor(status string) tea.Cmd {
	store, path := s.recoveryStore, s.path
	readerCmd := s.startReading(s.snapshot)
	s.status = status
	if store == nil {
		s.draftStored = false
		return readerCmd
	}
	discardCmd := func() tea.Msg {
		return documentDraftDiscardedMsg{err: store.DeleteRecovery(context.Background(), path)}
	}
	return tea.Batch(readerCmd, discardCmd)
}

// draftDiscarded records the result of deleting the draft on leaving the
// editor. On failure the draft is still known to exist, so the next clean
// autosave retries the deletion.
func (s *documentsScreen) draftDiscarded(msg documentDraftDiscardedMsg) {
	if msg.err == nil {
		s.draftStored = false
		return
	}
	s.draftStored = true
	s.errMessage = "Could not remove recovery draft: " + msg.err.Error()
}
