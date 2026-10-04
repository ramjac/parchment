package presentation

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
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
	ListPresentations(context.Context) ([]Presentation, error)
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

func (s *Service) List(ctx context.Context) ([]Presentation, error) {
	items, err := s.repository.ListPresentations(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].ModifiedAt.After(items[j].ModifiedAt)
	})
	return items, nil
}

func (s *Service) Get(ctx context.Context, id string) (Presentation, error) {
	return s.repository.GetPresentation(ctx, id)
}

func (s *Service) Create(ctx context.Context, title, source string) (Presentation, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Presentation{}, errors.New("presentation title is required")
	}
	if strings.TrimSpace(source) == "" {
		source = "# " + title + "\n\n## Slide 1\n\n"
	}
	deck, err := Parse(source)
	if err != nil {
		return Presentation{}, err
	}
	if deck.Title != title {
		return Presentation{}, fmt.Errorf("Markdown title %q does not match presentation title %q", deck.Title, title)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Presentation{}, fmt.Errorf("generate presentation ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := s.now().UTC()
	item := Presentation{
		Artifact: artifact.Artifact{
			ID: id, Kind: artifact.PresentationKind, Title: title,
			CreatedAt: now, ModifiedAt: now, FormatVersion: artifact.FormatVersion,
			Location: ".parchment/artifacts/" + id + "/content.md",
		},
		Version: FileVersion, Source: source,
	}
	if err := Validate(item); err != nil {
		return Presentation{}, err
	}
	if err := s.change(ctx, nil, &item, "Create presentation"); err != nil {
		return Presentation{}, err
	}
	return clonePresentation(item), nil
}

// Update replaces the presentation source if expected still matches storage.
func (s *Service) Update(ctx context.Context, expected Presentation, source string) (Presentation, error) {
	deck, err := Parse(source)
	if err != nil {
		return Presentation{}, err
	}
	if expected.Source == source && expected.Title == deck.Title {
		return expected, nil
	}
	after := clonePresentation(expected)
	after.Title = deck.Title
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

func (s *Service) Delete(ctx context.Context, id string) error {
	before, err := s.repository.GetPresentation(ctx, id)
	if err != nil {
		return err
	}
	return s.change(ctx, &before, nil, "Delete presentation")
}

func (s *Service) Undo(ctx context.Context) (string, error) { return s.history.Undo(ctx) }
func (s *Service) Redo(ctx context.Context) (string, error) { return s.history.Redo(ctx) }

// Parse parses the presentation-oriented subset of Go present's Markdown
// syntax: an H1 title, H2 slide headings, H3 subsections, // comments, and
// : speaker-note lines. Markdown text is preserved for the view layer.
func Parse(source string) (Deck, error) {
	source = artifactfile.StripPrivateBlocks(source)
	var deck Deck
	scanner := bufio.NewScanner(strings.NewReader(source))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	lineNumber := 0
	inFence := false
	fenceMarker := ""
	fenceListIndent := 0
	var listIndents []int
	listIndent := 0
	var current *Slide
	var header, body, notes strings.Builder
	flush := func() {
		if current != nil {
			current.Body = strings.Trim(body.String(), "\r\n")
			current.Notes = strings.TrimSpace(notes.String())
			deck.Slides = append(deck.Slides, *current)
			current = nil
			body.Reset()
			notes.Reset()
		}
	}
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, "\uFEFF")
			trimmed = strings.TrimSpace(line)
		}
		if deck.Title == "" && trimmed != "" &&
			(isIndentedCode(line) || !strings.HasPrefix(trimmed, "# ")) {
			return Deck{}, fmt.Errorf("line %d: presentation must begin with a '# Title' heading", lineNumber)
		}
		if len(listIndents) > 0 {
			listIndent = listIndents[len(listIndents)-1]
		} else {
			listIndent = 0
		}
		listContent, itemIndent, isListItem := listItemContent(line, listIndent)
		marker := markdownFence(trimmed)
		markerIndent := 0
		if inFence && fenceListIndent > 0 {
			if trimmed != "" && leadingSpaces(line) < fenceListIndent {
				inFence, fenceMarker, fenceListIndent = false, "", 0
			} else if isListItem {
				marker = ""
			} else if leadingSpaces(line) >= fenceListIndent {
				content := line[fenceListIndent:]
				if !hasFourSpaceFenceIndent(content) {
					marker = markdownFence(strings.TrimSpace(content))
				} else {
					marker = ""
				}
				markerIndent = fenceListIndent
			} else {
				marker = ""
			}
		}
		if !inFence {
			if isListItem {
				for len(listIndents) > 0 && listIndents[len(listIndents)-1] >= itemIndent {
					listIndents = listIndents[:len(listIndents)-1]
				}
				listIndents = append(listIndents, itemIndent)
				if !hasFourSpaceFenceIndent(listContent) {
					marker, markerIndent = markdownFence(strings.TrimSpace(listContent)), itemIndent
				} else {
					marker = ""
				}
			} else if trimmed != "" {
				for len(listIndents) > 0 && leadingSpaces(line) < listIndents[len(listIndents)-1] {
					listIndents = listIndents[:len(listIndents)-1]
				}
			}
			if len(listIndents) > 0 {
				listIndent = listIndents[len(listIndents)-1]
			} else {
				listIndent = 0
			}
			if !isListItem && listIndent > 0 && leadingSpaces(line) >= listIndent {
				content := line[listIndent:]
				if !hasFourSpaceFenceIndent(content) {
					marker = markdownFence(strings.TrimSpace(content))
				} else {
					marker = ""
				}
				markerIndent = listIndent
			} else if !isListItem && marker == "" && !isIndentedCode(line) {
				marker = markdownFence(trimmed)
			}
		}
		listContinuation := !isListItem && listIndent > 0 && leadingSpaces(line) >= listIndent
		if marker != "" && (!isIndentedCode(line) || markerIndent > 0) {
			if !inFence {
				inFence, fenceMarker, fenceListIndent = true, marker, markerIndent
			} else if marker[0] == fenceMarker[0] && len(marker) >= len(fenceMarker) &&
				strings.TrimSpace(trimmed[len(marker):]) == "" {
				inFence, fenceMarker, fenceListIndent = false, "", 0
			}
			if current != nil {
				body.WriteString(line + "\n")
			} else if deck.Title != "" {
				header.WriteString(line + "\n")
			}
			continue
		}
		if inFence {
			if current != nil {
				body.WriteString(line + "\n")
			} else if deck.Title != "" {
				header.WriteString(line + "\n")
			}
			continue
		}
		if isIndentedCode(line) {
			if current != nil {
				body.WriteString(line + "\n")
			} else if deck.Title != "" {
				header.WriteString(line + "\n")
			}
			continue
		}
		if strings.HasPrefix(trimmed, "# ") && !listContinuation {
			if deck.Title != "" || current != nil {
				return Deck{}, fmt.Errorf("line %d: only one top-level presentation title is allowed", lineNumber)
			}
			deck.Title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ") &&
			!listContinuation {
			flush()
			title := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			title = stripAnchor(title)
			if title == "" {
				return Deck{}, fmt.Errorf("line %d: slide title is required", lineNumber)
			}
			current = &Slide{Title: title}
			continue
		}
		if deck.Title == "" {
			if trimmed != "" {
				return Deck{}, fmt.Errorf("line %d: presentation must begin with a '# Title' heading", lineNumber)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(line, ": ") {
			if current == nil {
				return Deck{}, fmt.Errorf("line %d: speaker notes must follow a slide heading", lineNumber)
			}
			notes.WriteString(strings.TrimPrefix(line, ": ") + "\n")
			continue
		}
		if current != nil {
			body.WriteString(line + "\n")
		} else {
			header.WriteString(line + "\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return Deck{}, fmt.Errorf("read presentation Markdown: %w", err)
	}
	if inFence {
		return Deck{}, errors.New("presentation contains an unclosed fenced code block")
	}
	flush()
	if deck.Title == "" {
		return Deck{}, errors.New("presentation title is required as a '# Title' heading")
	}
	if len(deck.Slides) == 0 {
		return Deck{}, errors.New("presentation must contain at least one '## Slide' heading")
	}
	deck.Header = strings.Trim(header.String(), "\r\n")
	return deck, nil
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
	deck, err := Parse(item.Source)
	if err != nil {
		return err
	}
	if deck.Title != item.Title {
		return errors.New("presentation metadata title does not match Markdown title")
	}
	return nil
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
	return output.String()
}

func clonePresentation(item Presentation) Presentation {
	item.Tags = append([]string(nil), item.Tags...)
	item.Links = append([]string(nil), item.Links...)
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
	if len(left.Tags) == 0 {
		left.Tags = nil
	}
	if len(right.Tags) == 0 {
		right.Tags = nil
	}
	if len(left.Links) == 0 {
		left.Links = nil
	}
	if len(right.Links) == 0 {
		right.Links = nil
	}
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
	id := ""
	if o.before != nil {
		id = o.before.ID
	} else if o.after != nil {
		id = o.after.ID
	} else {
		return errors.New("presentation operation has no artifact")
	}
	return o.repository.TransitionPresentation(ctx, id, expected, target)
}
