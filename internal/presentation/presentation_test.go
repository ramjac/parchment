package presentation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	"example.com/parchment/internal/artifact"
	"example.com/parchment/internal/artifactfile"
)

func TestParseMarkdownSlidesNotesCommentsAndCodeFences(t *testing.T) {
	source := `# Quarterly Review

Presenter Name
Summary: Internal results

## Introduction {#intro}

Visible **Markdown**.
// This comment is omitted.
: Mention the revised chart.
: Ask for questions.

### Details

` + "```go" + `
// Code comments remain visible.
## Not a slide heading
` + "```" + `

    // This indented code comment remains visible.
    ## Nor is this a slide heading

## Results

Second slide.
`
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if deck.Title != "Quarterly Review" || len(deck.Slides) != 2 {
		t.Fatalf("parsed deck = %+v", deck)
	}
	first := deck.Slides[0]
	if first.Title != "Introduction" || first.Notes != "Mention the revised chart.\nAsk for questions." {
		t.Fatalf("first slide = %+v", first)
	}
	for _, want := range []string{
		"Visible **Markdown**.", "### Details", "// Code comments remain visible.",
		"## Not a slide heading", "// This indented code comment remains visible.",
		"## Nor is this a slide heading",
	} {
		if !strings.Contains(first.Body, want) {
			t.Errorf("slide body missing %q:\n%s", want, first.Body)
		}
	}
	if strings.Contains(first.Body, "This comment is omitted") {
		t.Fatalf("present comment appears in slide body: %q", first.Body)
	}
	preview := Preview(deck)
	if strings.Contains(preview, "Ask for questions") || strings.Contains(preview, "This comment is omitted") {
		t.Fatalf("private notes or comments appear in preview:\n%s", preview)
	}
	if !strings.Contains(preview, "Presenter Name") || !strings.Contains(preview, "Second slide.") || !strings.Contains(preview, "\f") {
		t.Fatalf("preview did not separate slides:\n%s", preview)
	}
}

func TestPreviewStripsTerminalControlsButKeepsLayoutSeparators(t *testing.T) {
	deck := Deck{
		Title:  "Title\x1b[2J",
		Header: "Header\x07",
		Slides: []Slide{{Title: "Slide\u009b31m", Body: "Body\twith tab\n"}},
	}
	preview := Preview(deck)
	for _, r := range preview {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' && r != '\f' {
			t.Errorf("preview retained terminal control U+%04X: %q", r, preview)
		}
	}
	for _, want := range []string{"Title[2J", "Header", "Slide31m", "Body\twith tab", "\f"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lost %q: %q", want, preview)
		}
	}
}

func TestEncodeDecodeSinglePresentationFile(t *testing.T) {
	item := testPresentation()
	data, err := Encode(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "```parchment-meta\n") || !strings.Contains(string(data), "\n# Demo\n") {
		t.Fatalf("encoded file must contain metadata and editable Markdown: %s", data)
	}
	if strings.Contains(string(data), "separate") {
		t.Fatal("unexpected sidecar marker")
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(item, decoded) {
		t.Fatalf("round trip changed presentation:\noriginal: %+v\ndecoded: %+v", item, decoded)
	}
}

func TestValidateRejectsInvalidUTF8Source(t *testing.T) {
	item := testPresentation()
	item.Source = "# Demo\n\n## Slide\n" + string([]byte{0xff})
	if err := Validate(item); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid presentation source returned %v", err)
	}
}

func TestParserRejectsInvalidSlideStructure(t *testing.T) {
	for _, source := range []string{
		"Missing title\n\n## Slide\n",
		"# No slides\n",
		"# Title\n\n## \n",
		"# Title\n\n## Slide\n```go\nunclosed",
		"```go\ncode before title\n```\n\n# Title\n\n## Slide\nBody\n",
		"    # Indented heading\n\n# Title\n\n## Slide\nBody\n",
	} {
		if _, err := Parse(source); err == nil {
			t.Errorf("Parse(%q) accepted invalid source", source)
		}
	}
}

func TestLongerMarkdownFenceCannotCloseOnShorterFence(t *testing.T) {
	source := "# Code talk\n\n## Example\n\n````md\n```\n## still code\n````\n\n## After\n\nDone.\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 2 || !strings.Contains(deck.Slides[0].Body, "## still code") {
		t.Fatalf("fenced headings were parsed as slides: %+v", deck)
	}
}

func TestParseFencedCodeOpenedAfterListMarker(t *testing.T) {
	for _, test := range []struct {
		name, fence, code, close string
	}{
		{"bullet", "- ~~~go", "  ## not a slide", "  ~~~"},
		{"ordered", "10. ~~~go", "    ## not a slide", "    ~~~"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "# Code talk\n\n## Example\n\n" + test.fence + "\n" +
				test.code + "\n" + test.close + "\n\n## After\n\nDone.\n"
			deck, err := Parse(source)
			if err != nil {
				t.Fatal(err)
			}
			if len(deck.Slides) != 2 {
				t.Fatalf("parsed %d slides, want 2", len(deck.Slides))
			}
			for _, want := range []string{test.fence, "## not a slide", test.close} {
				if !strings.Contains(deck.Slides[0].Body, want) {
					t.Errorf("first slide body missing %q:\n%s", want, deck.Slides[0].Body)
				}
			}
		})
	}
}

func TestIndentedFenceContentDoesNotCloseListFence(t *testing.T) {
	for _, contentIndent := range []string{"      ", "  \t"} {
		source := "# List fence\n\n## Example\n\n- ```go\n" + contentIndent +
			"```\n  ```\n"
		deck, err := Parse(source)
		if err != nil {
			t.Fatalf("Parse with code indentation %q: %v", contentIndent, err)
		}
		if len(deck.Slides) != 1 || !strings.Contains(deck.Slides[0].Body, contentIndent+"```") {
			t.Fatalf("indented fence content was lost: %+v", deck)
		}
	}
}

func TestIndentedCodeFenceInListContinuationIsNotAnOpener(t *testing.T) {
	source := "# Indented list code\n\n## Example\n\n- Item\n      ```go\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 1 || !strings.Contains(deck.Slides[0].Body, "      ```go") {
		t.Fatalf("indented list code was not preserved: %+v", deck)
	}
}

func TestMixedSpaceTabIndentationIsIndentedCode(t *testing.T) {
	source := "# Mixed indentation\n\n## Example\n\n \t## code, not a slide\n \t# code, not a title\n \t```go\n \t## still code\n \t```\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 1 {
		t.Fatalf("parsed %d slides, want 1", len(deck.Slides))
	}
	for _, want := range []string{" \t## code, not a slide", " \t# code, not a title", " \t```go", " \t## still code", " \t```"} {
		if !strings.Contains(deck.Slides[0].Body, want) {
			t.Errorf("slide body missing %q:\n%s", want, deck.Slides[0].Body)
		}
	}
}

func TestExcessListIndentationDoesNotOpenFence(t *testing.T) {
	source := "# Indented list fence\n\n## Example\n\n-     ```go\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 1 || !strings.Contains(deck.Slides[0].Body, "-     ```go") {
		t.Fatalf("excess list indentation was not preserved as code: %+v", deck)
	}
}

func TestParseFencedCodeInDeeplyIndentedNestedList(t *testing.T) {
	source := "# Nested lists\n\n## Example\n\n- Parent\n    - ```go\n      fmt.Println(\"nested\")\n      ```\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 1 {
		t.Fatalf("parsed %d slides, want 1", len(deck.Slides))
	}
	for _, want := range []string{"    - ```go", "fmt.Println(\"nested\")", "      ```"} {
		if !strings.Contains(deck.Slides[0].Body, want) {
			t.Errorf("nested-list slide body missing %q:\n%s", want, deck.Slides[0].Body)
		}
	}
}

func TestParseKeepsHeadingsInListContinuationsOnCurrentSlide(t *testing.T) {
	source := "# List content\n\n## First slide\n\n- Item\n\n  ## Nested heading\n  # Nested title\n\n## Second slide\n\nEnd\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 2 {
		t.Fatalf("parsed %d slides, want 2", len(deck.Slides))
	}
	for _, want := range []string{"  ## Nested heading", "  # Nested title"} {
		if !strings.Contains(deck.Slides[0].Body, want) {
			t.Errorf("first slide lost list heading %q:\n%s", want, deck.Slides[0].Body)
		}
	}
}

func TestParseRestoresParentListContextAfterDedent(t *testing.T) {
	source := "# Nested list content\n\n## First slide\n\n- Outer\n  - Inner\n  ## Outer detail\n  # Outer title\n\n## Second slide\n\nEnd\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 2 {
		t.Fatalf("parsed %d slides, want 2", len(deck.Slides))
	}
	for _, want := range []string{"  ## Outer detail", "  # Outer title"} {
		if !strings.Contains(deck.Slides[0].Body, want) {
			t.Errorf("outer list continuation lost %q:\n%s", want, deck.Slides[0].Body)
		}
	}
}

func TestParsePreservesInlineTripleBackticks(t *testing.T) {
	source := "# Inline code\n\n## Slide\n\nText with ```inline code``` in the middle.\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 1 || !strings.Contains(deck.Slides[0].Body, "```inline code```") {
		t.Fatalf("inline backticks were not kept as slide text: %+v", deck)
	}
}

func TestIndentedFenceIsCodeAndHeaderFencesArePreserved(t *testing.T) {
	source := "# Code talk\n\n```go\nsample()\n```\n\n## Example\n\n    ```\n    ## not a slide\n    ```\n\n## After\n\nDone.\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(deck.Slides) != 2 {
		t.Fatalf("parsed %d slides, want 2", len(deck.Slides))
	}
	for _, want := range []string{"```go", "sample()", "```"} {
		if !strings.Contains(deck.Header, want) {
			t.Errorf("header missing fenced Markdown %q:\n%s", want, deck.Header)
		}
	}
	if !strings.Contains(deck.Slides[0].Body, "    ## not a slide") {
		t.Fatalf("indented code was not retained in the slide: %q", deck.Slides[0].Body)
	}
}

func TestParsePreservesIndentationAtMarkdownBoundaries(t *testing.T) {
	source := "# Indented\n\n    header code\n\n## Slide\n\n    slide code\n"
	deck, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(deck.Header, "    header code") {
		t.Fatalf("header indentation was lost: %q", deck.Header)
	}
	if got := deck.Slides[0].Body; !strings.HasPrefix(got, "    slide code") {
		t.Fatalf("slide body indentation was lost: %q", got)
	}
}

func TestUpdateUsesExpectedSnapshotAndUndoRedo(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service := NewService(repository, 10)
	item, err := service.Create(ctx, "Demo", "# Demo\n\n## Start\n\nBefore\n")
	if err != nil {
		t.Fatal(err)
	}
	stale := item
	updated, err := service.Update(ctx, item, "# Demo\n\n## Start\n\nAfter\n")
	if err != nil || updated.Source == item.Source {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	if _, err := service.Update(ctx, stale, "# Demo\n\n## Start\n\nStale\n"); err == nil {
		t.Fatal("stale update overwrote a later change")
	}
	if _, err := service.Undo(ctx); err != nil {
		t.Fatal(err)
	}
	undone, err := service.Get(ctx, item.ID)
	if err != nil || undone.Source != item.Source {
		t.Fatalf("undo = %+v, %v", undone, err)
	}
	if _, err := service.Redo(ctx); err != nil {
		t.Fatal(err)
	}
	redone, err := service.Get(ctx, item.ID)
	if err != nil || redone.Source != updated.Source {
		t.Fatalf("redo = %+v, %v", redone, err)
	}
	renamed, err := service.Update(ctx, redone, "# Renamed\n\n## Start\n\nAfter\n")
	if err != nil || renamed.Title != "Renamed" {
		t.Fatalf("Markdown title update = %+v, %v", renamed, err)
	}
}

var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func testPresentation() Presentation {
	return Presentation{
		Artifact: artifact.Artifact{
			ID: "0123456789abcdef0123456789abcdef", Kind: artifact.PresentationKind,
			Title: "Demo", CreatedAt: testNow, ModifiedAt: testNow,
			FormatVersion: artifact.FormatVersion,
			Location:      ".parchment/artifacts/0123456789abcdef0123456789abcdef/content.md",
		},
		Version: FileVersion, Source: "# Demo\n\n## Slide 1\n\nHello.\n",
	}
}

type memoryRepository struct{ items map[string]Presentation }

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{items: make(map[string]Presentation)}
}
func (r *memoryRepository) ListPresentations(context.Context) ([]Presentation, error) {
	items := make([]Presentation, 0, len(r.items))
	for _, item := range r.items {
		items = append(items, clonePresentation(item))
	}
	return items, nil
}
func (r *memoryRepository) GetPresentation(_ context.Context, id string) (Presentation, error) {
	item, ok := r.items[id]
	if !ok {
		return Presentation{}, ErrNotFound
	}
	return clonePresentation(item), nil
}
func (r *memoryRepository) TransitionPresentation(_ context.Context, id string, expected, target *Presentation) error {
	current, exists := r.items[id]
	if expected == nil {
		if exists {
			return errors.New("already exists")
		}
	} else if !exists || !Equal(current, *expected) {
		return errors.New("stale presentation")
	}
	if target == nil {
		delete(r.items, id)
		return nil
	}
	r.items[id] = clonePresentation(*target)
	return nil
}

func TestMetadataBlockIsInspectable(t *testing.T) {
	data, err := Encode(testPresentation())
	if err != nil {
		t.Fatal(err)
	}
	file, err := artifactfile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if file.Artifact.Title != "Demo" || file.Artifact.Kind != artifact.PresentationKind {
		t.Fatalf("embedded metadata = %+v", file.Artifact)
	}
}

func TestEqualTreatsEmptyMetadataSlicesAsNil(t *testing.T) {
	left := testPresentation()
	right := left
	right.Tags = []string{}
	right.Links = []string{}
	if !Equal(left, right) {
		t.Fatal("empty tags and links should equal nil slices")
	}
}
