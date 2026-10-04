package document

import (
	"fmt"
	"strings"
	"testing"
)

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
		level int
		title string
	}{{1, "Title"}, {2, "Part One"}, {3, "Deep"}, {2, "Last"}}
	if len(sections) != len(want) {
		t.Fatalf("sections = %+v", sections)
	}
	for i, w := range want {
		s := sections[i]
		if s.Level != w.level || s.Title != w.title {
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
