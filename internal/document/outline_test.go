package document

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestReaderOutlineContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ReaderOutlineContext(ctx, Document{Body: "# Heading", Layout: DefaultLayout()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled outline returned %v", err)
	}
}

func TestReaderOutlineMatchesPrintOutline(t *testing.T) {
	parts := []string{
		"# Duplicate", "## Duplicate", "# ` `", "### ` `", "# [Link](https://example.com)",
		"plain text", "Exclamation!", "  \t", "\u00a0", "setext title", "-----",
		"\t- item", "1. numbered", "+ item", "**bold**", "[label](target)", "`code`",
		"", "```", "```parchment-hidden", "~~~", "# code", "```", "<a id=\"target\"></a>",
		"<!-- comment -->", "- list item", "> quote", "| table |", "paragraph <a id=\"inline\"></a>",
		"<A ID=\"capital\"></A>",
		"<!-- parchment:page-break -->", "<!-- parchment:section-break columns=2 -->",
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		var lines []string
		for j := 0; j < 60; j++ {
			lines = append(lines, parts[rng.Intn(len(parts))])
		}
		layout := DefaultLayout()
		layout.Columns = 1 + rng.Intn(3)
		separator := "\n"
		if i%2 != 0 {
			separator = "\r\n"
		}
		doc := Document{Body: strings.Join(lines, separator), Layout: layout}
		want, err := Outline(doc)
		if err != nil {
			t.Fatal(err)
		}
		var filtered []Section
		for _, section := range want {
			if !section.SectionBreak {
				section.Page = 0
				filtered = append(filtered, section)
			}
		}
		got, err := ReaderOutline(doc)
		if err != nil || !reflect.DeepEqual(got, filtered) {
			t.Fatalf("case %d: got %+v, want %+v; err=%v\n%s", i, got, filtered, err, doc.Body)
		}
	}
}

func TestOutlineListsHeadingsWithPages(t *testing.T) {
	var body strings.Builder
	body.WriteString("# Title\n\nintro\n\n```\n# not a heading\n```\n\n## Part **One**\n\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&body, "line %d\n\n", i)
	}
	body.WriteString("### Deep\n\n<!-- parchment:page-break -->\n\n## Last\n\ntext\n")
	d := Document{Body: body.String(), Layout: DefaultLayout()}
	sections, err := Outline(d)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := Paginate(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		level        int
		title        string
		slug         string
		anchor       bool
		sectionBreak bool
		columns      int
	}{
		{1, "Title", "title", false, false, 0},
		{2, "Part One", "part-one", false, false, 0},
		{3, "Deep", "deep", false, false, 0},
		{2, "Last", "last", false, false, 0},
	}
	if len(sections) != len(want) {
		t.Fatalf("sections = %+v", sections)
	}
	for i, w := range want {
		s := sections[i]
		if s.Level != w.level || s.Title != w.title || s.Slug != w.slug || s.Anchor != w.anchor || s.SectionBreak != w.sectionBreak || s.Columns != w.columns {
			t.Fatalf("section %d = %+v, want %+v", i, s, w)
		}
		if s.Page < 1 || s.Page > len(pages) {
			t.Fatalf("section %d page %d out of range", i, s.Page)
		}
		text := strings.ToUpper(strings.Join(pages[s.Page-1].Lines, "\n"))
		if !strings.Contains(text, strings.ToUpper(w.title)) {
			t.Fatalf("section %q not on page %d", w.title, s.Page)
		}
	}
	if sections[0].Page != 1 || sections[3].Page != len(pages) || sections[3].Page < 3 {
		t.Fatalf("pages = %+v of %d", sections, len(pages))
	}
}

func TestOutlineEmptyDocument(t *testing.T) {
	sections, err := Outline(Document{Layout: DefaultLayout()})
	if err != nil || len(sections) != 0 {
		t.Fatalf("sections=%v err=%v", sections, err)
	}
}

func TestOutlineIncludesSetextHeadings(t *testing.T) {
	d := Document{Body: "Short title\n-----------\n\ntext\n\nBig title\n=========\n\n- item\n\n---\n", Layout: DefaultLayout()}
	sections, err := Outline(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Level != 2 || sections[0].Title != "Short title" ||
		sections[1].Level != 1 || sections[1].Title != "Big title" {
		t.Fatalf("sections = %+v", sections)
	}
	pages, _ := Paginate(d)
	text := strings.Join(pages[0].Lines, "\n")
	if !strings.Contains(text, "BIG TITLE") || !strings.Contains(text, "=========") {
		t.Fatalf("setext heading not rendered as a heading:\n%s", text)
	}
}

func TestOutlineSluggingAndAnchors(t *testing.T) {
	d := Document{
		Body:   "# Duplicate\n\n## Duplicate\n\n### Duplicate\n\nSome text with <a id=\"explicit-anchor\"></a> in it.\n\n<!-- parchment:section-break columns=2 continuous -->\n\n## Normal",
		Layout: DefaultLayout(),
	}
	sections, err := Outline(d)
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		level        int
		title        string
		slug         string
		anchor       bool
		sectionBreak bool
		columns      int
	}{
		{1, "Duplicate", "duplicate", false, false, 0},
		{2, "Duplicate", "duplicate-1", false, false, 0},
		{3, "Duplicate", "duplicate-2", false, false, 0},
		{0, "", "explicit-anchor", true, false, 0},
		{0, "── Section (2 cols)", "", false, true, 2},
		{2, "Normal", "normal", false, false, 0},
	}

	if len(sections) != len(want) {
		t.Fatalf("got %d sections, want %d: %+v", len(sections), len(want), sections)
	}
	for i, w := range want {
		s := sections[i]
		if s.Level != w.level || s.Title != w.title || s.Slug != w.slug || s.Anchor != w.anchor || s.SectionBreak != w.sectionBreak || (s.SectionBreak && s.Columns != w.columns) {
			t.Fatalf("section %d:\n got: %+v\nwant: %+v", i, s, w)
		}
	}
}
