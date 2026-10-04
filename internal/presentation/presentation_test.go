package presentation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
