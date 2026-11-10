package presentation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
	"example.com/parchment/internal/history"
)

const FileVersion = 1

var ErrNotFound = errors.New("presentation not found")

// Slide is a Markdown section with presentation-only speaker notes.
type Slide struct {
	Title string
	Body  string
	Notes string
}

// Deck contains the parsed presentation structure.
type Deck struct {
	Title  string
	Header string
	Slides []Slide
}

// Presentation stores a complete Markdown presentation in one file.
type Presentation struct {
	artifact.Artifact
	Version int
	Source  string
	Blocks  map[string]json.RawMessage `json:"-"`
}

type fileContent struct {
	Version int `json:"version"`
}

// Repository persists presentations as single-file artifacts.
type Repository interface {
	GetPresentation(context.Context, string) (Presentation, error)
	TransitionPresentation(context.Context, string, *Presentation, *Presentation) error
}

// Service applies presentation changes and tracks undo and redo in memory.
type Service struct {
	repository Repository
	history    *history.Stack
	now        func() time.Time
}

func NewService(repository Repository, undoLimit int) *Service {
	return &Service{
		repository: repository, history: history.New(undoLimit),
		now: func() time.Time { return time.Now().UTC() },
	}
}

// Get loads the presentation stored at path.
func (s *Service) Get(ctx context.Context, path string) (Presentation, error) {
	return s.repository.GetPresentation(ctx, path)
}

// Create writes a new presentation file at path. Creation is not undoable.
func (s *Service) Create(ctx context.Context, path, source string) (Presentation, error) {
	if strings.TrimSpace(source) == "" {
		title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if strings.TrimSpace(title) == "" {
			title = "Presentation"
		}
		source = "# " + title + "\n\n## Slide 1\n\n"
	}
	if _, err := Parse(source); err != nil {
		return Presentation{}, err
	}
	id, err := artifact.NewID(artifact.PresentationKind)
	if err != nil {
		return Presentation{}, err
	}
	now := s.now().UTC()
	item := Presentation{
		Artifact: artifact.Artifact{
			ID: id, Kind: artifact.PresentationKind,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Path: path,
		},
		Version: FileVersion, Source: source,
	}
	if err := Validate(item); err != nil {
		return Presentation{}, err
	}
	if err := s.repository.TransitionPresentation(ctx, path, nil, &item); err != nil {
		return Presentation{}, err
	}
	return s.repository.GetPresentation(ctx, path)
}

// Update replaces the presentation source if expected still matches storage.
func (s *Service) Update(ctx context.Context, expected Presentation, source string) (Presentation, error) {
	if _, err := Parse(source); err != nil {
		return Presentation{}, err
	}
	if expected.Source == source {
		return expected, nil
	}
	after := clonePresentation(expected)
	after.Source = source
	after.ModifiedAt = s.now().UTC()
	if err := Validate(after); err != nil {
		return Presentation{}, err
	}
	if err := s.change(ctx, &expected, &after, "Edit presentation"); err != nil {
		return Presentation{}, err
	}
	return clonePresentation(after), nil
}

func (s *Service) Undo(ctx context.Context) (string, error) { return s.history.Undo(ctx) }
func (s *Service) Redo(ctx context.Context) (string, error) { return s.history.Redo(ctx) }

type parser struct {
	deck            Deck
	lineNumber      int
	inFence         bool
	fenceMarker     string
	fenceListIndent int
	listIndents     []int
	current         *Slide
	header          strings.Builder
	body            strings.Builder
	notes           strings.Builder
}

func (p *parser) flush() {
	if p.current != nil {
		p.current.Body = strings.Trim(p.body.String(), "\r\n")
		p.current.Notes = strings.TrimSpace(p.notes.String())
		p.deck.Slides = append(p.deck.Slides, *p.current)
		p.current = nil
		p.body.Reset()
		p.notes.Reset()
	}
}

func (p *parser) writeContent(line string) {
	if p.current != nil {
		p.body.WriteString(line + "\n")
	} else if p.deck.Title != "" {
		p.header.WriteString(line + "\n")
	}
}

func (p *parser) prepareLine(rawLine string) (line, trimmed string, err error) {
	p.lineNumber++
	line = strings.TrimSuffix(rawLine, "\r")
	if p.lineNumber == 1 {
		line = strings.TrimPrefix(line, "\uFEFF")
	}
	trimmed = strings.TrimSpace(line)
	if p.deck.Title == "" && trimmed != "" &&
		(isIndentedCode(line) || !strings.HasPrefix(trimmed, "# ")) {
		return "", "", fmt.Errorf("line %d: presentation must begin with a '# Title' heading", p.lineNumber)
	}
	return line, trimmed, nil
}

func (p *parser) evalFenceState(line, trimmed string) (handled bool, listContinuation bool) {
	listIndent := currentListIndent(p.listIndents)
	listContent, itemIndent, isListItem := listItemContent(line, listIndent)
	marker := markdownFence(trimmed)
	markerIndent := 0

	if p.inFence && p.fenceListIndent > 0 {
		if trimmed != "" && leadingSpaces(line) < p.fenceListIndent {
			p.inFence, p.fenceMarker, p.fenceListIndent = false, "", 0
		} else {
			marker, markerIndent = evalListFenceMarker(line, p.fenceListIndent, isListItem)
		}
	}

	if !p.inFence {
		p.listIndents = updateListIndents(p.listIndents, line, trimmed, itemIndent, isListItem)
		listIndent = currentListIndent(p.listIndents)
		marker, markerIndent = evalOutsideFenceMarker(line, trimmed, listContent, listIndent, itemIndent, isListItem, marker)
	}

	listContinuation = !isListItem && listIndent > 0 && leadingSpaces(line) >= listIndent

	if marker != "" && (!isIndentedCode(line) || markerIndent > 0) {
		if !p.inFence {
			p.inFence, p.fenceMarker, p.fenceListIndent = true, marker, markerIndent
		} else if marker[0] == p.fenceMarker[0] && len(marker) >= len(p.fenceMarker) &&
			strings.TrimSpace(trimmed[len(marker):]) == "" {
			p.inFence, p.fenceMarker, p.fenceListIndent = false, "", 0
		}
		p.writeContent(line)
		return true, listContinuation
	}

	if p.inFence || isIndentedCode(line) {
		p.writeContent(line)
		return true, listContinuation
	}

	return false, listContinuation
}

func (p *parser) parseTitleHeading(trimmed string) error {
	if p.deck.Title != "" || p.current != nil {
		return fmt.Errorf("line %d: only one top-level presentation title is allowed", p.lineNumber)
	}
	p.deck.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
	return nil
}

func (p *parser) parseSlideHeading(trimmed string) error {
	p.flush()
	title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
	title = stripAnchor(title)
	if title == "" {
		return fmt.Errorf("line %d: slide title is required", p.lineNumber)
	}
	p.current = &Slide{Title: title}
	return nil
}

func (p *parser) parseSpeakerNote(line string) error {
	if p.current == nil {
		return fmt.Errorf("line %d: speaker notes must follow a slide heading", p.lineNumber)
	}
	p.notes.WriteString(strings.TrimPrefix(line, ": ") + "\n")
	return nil
}

func (p *parser) parseLine(rawLine string) error {
	line, trimmed, err := p.prepareLine(rawLine)
	if err != nil {
		return err
	}

	handled, listContinuation := p.evalFenceState(line, trimmed)
	if handled {
		return nil
	}

	if strings.HasPrefix(trimmed, "# ") && !listContinuation {
		return p.parseTitleHeading(trimmed)
	}
	if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") && !listContinuation {
		return p.parseSlideHeading(trimmed)
	}
	if p.deck.Title == "" {
		if trimmed != "" {
			return fmt.Errorf("line %d: presentation must begin with a '# Title' heading", p.lineNumber)
		}
		return nil
	}
	if strings.HasPrefix(trimmed, "//") {
		return nil
	}
	if strings.HasPrefix(line, ": ") {
		return p.parseSpeakerNote(line)
	}

	p.writeContent(line)
	return nil
}

func (p *parser) finish() (Deck, error) {
	if p.inFence {
		return Deck{}, errors.New("presentation contains an unclosed fenced code block")
	}
	p.flush()
	if p.deck.Title == "" {
		return Deck{}, errors.New("presentation title is required as a '# Title' heading")
	}
	if len(p.deck.Slides) == 0 {
		return Deck{}, errors.New("presentation must contain at least one '## Slide' heading")
	}
	p.deck.Header = strings.Trim(p.header.String(), "\r\n")
	return p.deck, nil
}

func (p *parser) parseSource(source string) (Deck, error) {
	scanner := bufio.NewScanner(strings.NewReader(source))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		if err := p.parseLine(scanner.Text()); err != nil {
			return Deck{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Deck{}, fmt.Errorf("read presentation Markdown: %w", err)
	}
	return p.finish()
}

// Parse parses the presentation-oriented subset of Go present's Markdown
// syntax: an H1 title, H2 slide headings, H3 subsections, // comments, and
// : speaker-note lines. Markdown text is preserved for the view layer.
func Parse(source string) (Deck, error) {
	source = artifactfile.StripPrivateBlocks(source)
	var p parser
	return p.parseSource(source)
}

func currentListIndent(listIndents []int) int {
	if len(listIndents) > 0 {
		return listIndents[len(listIndents)-1]
	}
	return 0
}

func updateListIndents(listIndents []int, line, trimmed string, itemIndent int, isListItem bool) []int {
	if isListItem {
		for len(listIndents) > 0 && listIndents[len(listIndents)-1] >= itemIndent {
			listIndents = listIndents[:len(listIndents)-1]
		}
		return append(listIndents, itemIndent)
	}
	if trimmed != "" {
		for len(listIndents) > 0 && leadingSpaces(line) < listIndents[len(listIndents)-1] {
			listIndents = listIndents[:len(listIndents)-1]
		}
	}
	return listIndents
}

func evalListFenceMarker(line string, fenceListIndent int, isListItem bool) (string, int) {
	if isListItem || leadingSpaces(line) < fenceListIndent {
		return "", 0
	}
	content := line[fenceListIndent:]
	if !hasFourSpaceFenceIndent(content) {
		return markdownFence(strings.TrimSpace(content)), fenceListIndent
	}
	return "", 0
}

func evalOutsideFenceMarker(line, trimmed, listContent string, listIndent, itemIndent int, isListItem bool, initialMarker string) (string, int) {
	if isListItem {
		if !hasFourSpaceFenceIndent(listContent) {
			return markdownFence(strings.TrimSpace(listContent)), itemIndent
		}
		return "", 0
	}
	if listIndent > 0 && leadingSpaces(line) >= listIndent {
		content := line[listIndent:]
		if !hasFourSpaceFenceIndent(content) {
			return markdownFence(strings.TrimSpace(content)), listIndent
		}
		return "", 0
	}
	if initialMarker == "" && !isIndentedCode(line) {
		return markdownFence(trimmed), 0
	}
	return initialMarker, 0
}

func isIndentedCode(line string) bool {
	return hasFourSpaceFenceIndent(line)
}

func markdownFence(line string) string {
	if len(line) < 3 || (line[0] != '`' && line[0] != '~') {
		return ""
	}
	i := 1
	for i < len(line) && line[i] == line[0] {
		i++
	}
	if i < 3 {
		return ""
	}
	if line[0] == '`' && strings.ContainsRune(line[i:], '`') {
		return ""
	}
	return line[:i]
}

func listItemContent(line string, parentIndent int) (string, int, bool) {
	maxIndent := 3 + parentIndent
	indent := 0
	for indent < len(line) && indent <= maxIndent && line[indent] == ' ' {
		indent++
	}
	if indent > maxIndent || indent == len(line) {
		return "", 0, false
	}
	end := indent
	if line[end] == '-' || line[end] == '+' || line[end] == '*' {
		end++
	} else {
		digits := end
		for end < len(line) && line[end] >= '0' && line[end] <= '9' && end-digits < 10 {
			end++
		}
		if end == digits || end == len(line) || line[end] != '.' && line[end] != ')' {
			return "", 0, false
		}
		end++
	}
	if end == len(line) || line[end] != ' ' && line[end] != '\t' {
		return "", 0, false
	}
	end++
	return line[end:], end, true
}

func leadingSpaces(line string) int {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	return indent
}

func hasFourSpaceFenceIndent(line string) bool {
	indent := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			indent++
			if indent >= 4 {
				return true
			}
		case '\t':
			return true
		default:
			return false
		}
	}
	return false
}

func stripAnchor(title string) string {
	if index := strings.LastIndex(title, " {#"); index >= 0 && strings.HasSuffix(title, "}") {
		return strings.TrimSpace(title[:index])
	}
	return title
}

// Validate checks the shared artifact envelope and the embedded Markdown.
func Validate(item Presentation) error {
	if err := item.Artifact.Validate(); err != nil {
		return err
	}
	if item.Kind != artifact.PresentationKind {
		return errors.New("artifact is not a presentation")
	}
	if item.Version != FileVersion {
		return fmt.Errorf("unsupported presentation version %d", item.Version)
	}
	if !utf8.ValidString(item.Source) {
		return errors.New("presentation source must be valid UTF-8")
	}
	_, err := Parse(item.Source)
	return err
}

// Encode writes a self-contained Markdown file with a leading metadata block.
func Encode(item Presentation) ([]byte, error) {
	if err := Validate(item); err != nil {
		return nil, err
	}
	blocks := make(map[string]any, len(item.Blocks)+1)
	for name, payload := range item.Blocks {
		blocks[name] = payload
	}
	blocks["parchment-presentation"] = fileContent{Version: item.Version}
	return artifactfile.Encode(item.Artifact, item.Source, blocks)
}

// Decode reads metadata and Markdown from a single presentation file.
func Decode(data []byte) (Presentation, error) {
	file, err := artifactfile.Decode(data)
	if err != nil {
		return Presentation{}, fmt.Errorf("decode presentation file: %w", err)
	}
	if file.Artifact.Kind != artifact.PresentationKind {
		return Presentation{}, ErrNotFound
	}
	content, ok := file.Blocks["parchment-presentation"]
	if !ok {
		return Presentation{}, errors.New("presentation data block is missing")
	}
	var payload fileContent
	if err := json.Unmarshal(content, &payload); err != nil {
		return Presentation{}, fmt.Errorf("decode presentation data: %w", err)
	}
	if payload.Version != FileVersion {
		return Presentation{}, fmt.Errorf("unsupported presentation file version %d", payload.Version)
	}
	item := Presentation{
		Artifact: file.Artifact, Version: payload.Version, Source: file.Body,
		Blocks: cloneBlocksExcept(file.Blocks, "parchment-presentation"),
	}
	item.CreatedAt = item.CreatedAt.UTC()
	item.ModifiedAt = item.ModifiedAt.UTC()
	if err := Validate(item); err != nil {
		return Presentation{}, fmt.Errorf("validate presentation: %w", err)
	}
	return item, nil
}

// Preview returns slide text without speaker notes or comments. It deliberately
// leaves Markdown syntax intact rather than storing rendered output.
func Preview(deck Deck) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s\n%s\n", deck.Title, strings.Repeat("=", len([]rune(deck.Title))))
	if deck.Header != "" {
		output.WriteString("\n" + deck.Header + "\n")
	}
	for _, slide := range deck.Slides {
		output.WriteString("\n\f\n")
		fmt.Fprintf(&output, "%s\n%s\n\n", slide.Title, strings.Repeat("=", len([]rune(slide.Title))))
		output.WriteString(slide.Body)
		if !strings.HasSuffix(slide.Body, "\n") {
			output.WriteByte('\n')
		}
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			return -1
		}
		return r
	}, output.String())
}

func clonePresentation(item Presentation) Presentation {
	item.Blocks = cloneBlocks(item.Blocks)
	return item
}

func cloneBlocks(blocks map[string]json.RawMessage) map[string]json.RawMessage {
	return cloneBlocksExcept(blocks)
}

func cloneBlocksExcept(blocks map[string]json.RawMessage, excluded ...string) map[string]json.RawMessage {
	if len(blocks) == 0 {
		return nil
	}
	exclude := make(map[string]bool, len(excluded))
	for _, name := range excluded {
		exclude[name] = true
	}
	clone := make(map[string]json.RawMessage, len(blocks))
	for name, payload := range blocks {
		if exclude[name] {
			continue
		}
		clone[name] = append(json.RawMessage(nil), payload...)
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}

func Equal(left, right Presentation) bool {
	left.Title, right.Title = "", ""
	return reflect.DeepEqual(left, right)
}

func (s *Service) change(ctx context.Context, before, after *Presentation, description string) error {
	return s.history.Execute(ctx, presentationOperation{
		repository: s.repository, before: clonePresentationPointer(before),
		after: clonePresentationPointer(after), description: description,
	})
}

func clonePresentationPointer(item *Presentation) *Presentation {
	if item == nil {
		return nil
	}
	clone := clonePresentation(*item)
	return &clone
}

type presentationOperation struct {
	repository  Repository
	before      *Presentation
	after       *Presentation
	description string
}

func (o presentationOperation) Apply(ctx context.Context) error {
	return o.transition(ctx, o.before, o.after)
}
func (o presentationOperation) Undo(ctx context.Context) error {
	return o.transition(ctx, o.after, o.before)
}
func (o presentationOperation) Description() string { return o.description }
func (o presentationOperation) transition(ctx context.Context, expected, target *Presentation) error {
	if o.before == nil || o.after == nil {
		return errors.New("presentation operation must have a before and after value")
	}
	return o.repository.TransitionPresentation(ctx, o.before.Path, expected, target)
}
