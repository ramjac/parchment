package document

import (
	"testing"
)

func TestSectionBreakMarkup(t *testing.T) {
	tests := []struct {
		name       string
		columns    int
		continuous bool
		want       string
	}{
		{
			name:       "default columns, not continuous",
			columns:    1,
			continuous: false,
			want:       "<!-- parchment:section-break columns=1 -->",
		},
		{
			name:       "multiple columns, not continuous",
			columns:    2,
			continuous: false,
			want:       "<!-- parchment:section-break columns=2 -->",
		},
		{
			name:       "default columns, continuous",
			columns:    1,
			continuous: true,
			want:       "<!-- parchment:section-break columns=1 continuous -->",
		},
		{
			name:       "multiple columns, continuous",
			columns:    3,
			continuous: true,
			want:       "<!-- parchment:section-break columns=3 continuous -->",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SectionBreakMarkup(tt.columns, tt.continuous)
			if got != tt.want {
				t.Errorf("SectionBreakMarkup(%d, %v) = %q; want %q", tt.columns, tt.continuous, got, tt.want)
			}
		})
	}
}

func TestParseDirective(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantOk   bool
		wantKind directiveKind
		wantCols int
		wantCont bool
	}{
		{
			name:     "page break",
			line:     "<!-- parchment:page-break -->",
			wantOk:   true,
			wantKind: pageBreak,
		},
		{
			name:     "page break with spaces",
			line:     "  <!--   parchment:page-break   -->  ",
			wantOk:   true,
			wantKind: pageBreak,
		},
		{
			name:     "section break default",
			line:     "<!-- parchment:section-break -->",
			wantOk:   true,
			wantKind: sectionBreak,
		},
		{
			name:     "section break columns",
			line:     "<!-- parchment:section-break columns=2 -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCols: 2,
		},
		{
			name:     "section break continuous",
			line:     "<!-- parchment:section-break continuous -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCont: true,
		},
		{
			name:     "section break columns and continuous",
			line:     "<!-- parchment:section-break columns=3 continuous -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCols: 3,
			wantCont: true,
		},
		{
			name:     "section break continuous and columns",
			line:     "<!-- parchment:section-break continuous columns=2 -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCols: 2,
			wantCont: true,
		},
		{
			name:     "invalid columns (too high clamped to MaxColumns)",
			line:     "<!-- parchment:section-break columns=10 -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCols: MaxColumns,
		},
		{
			name:     "invalid columns (too low clamped to 1)",
			line:     "<!-- parchment:section-break columns=0 -->",
			wantOk:   true,
			wantKind: sectionBreak,
			wantCols: 1,
		},
		{
			name:   "invalid columns string (regex fail)",
			line:   "<!-- parchment:section-break columns=foo -->",
			wantOk: false,
		},
		{
			name:   "unknown attribute (regex fail)",
			line:   "<!-- parchment:section-break unknown=true -->",
			wantOk: false,
		},
		{
			name:     "unknown attribute matching regex",
			line:     "<!-- parchment:section-break unknown -->",
			wantOk:   true,
			wantKind: sectionBreak,
		},
		{
			name:   "invalid directive",
			line:   "<!-- parchment:invalid -->",
			wantOk: false,
		},
		{
			name:   "ordinary comment",
			line:   "<!-- just a comment -->",
			wantOk: false,
		},
		{
			name:   "ordinary text",
			line:   "hello world",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDirective(tt.line)
			if ok != tt.wantOk {
				t.Fatalf("parseDirective(%q) ok = %v; want %v", tt.line, ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if got.kind != tt.wantKind {
				t.Errorf("parseDirective(%q) kind = %v; want %v", tt.line, got.kind, tt.wantKind)
			}
			if got.columns != tt.wantCols {
				t.Errorf("parseDirective(%q) columns = %d; want %d", tt.line, got.columns, tt.wantCols)
			}
			if got.continuous != tt.wantCont {
				t.Errorf("parseDirective(%q) continuous = %v; want %v", tt.line, got.continuous, tt.wantCont)
			}
		})
	}
}

func TestFenceOpener(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantMarker string
		wantOk     bool
	}{
		{
			name:       "backticks",
			line:       "```go",
			wantMarker: "```",
			wantOk:     true,
		},
		{
			name:       "backticks with leading spaces",
			line:       "  ```",
			wantMarker: "```",
			wantOk:     true,
		},
		{
			name:       "tildes",
			line:       "~~~bash",
			wantMarker: "~~~",
			wantOk:     true,
		},
		{
			name:       "tildes with leading spaces",
			line:       "  ~~~",
			wantMarker: "~~~",
			wantOk:     true,
		},
		{
			name:   "not a fence (fewer backticks)",
			line:   "``go",
			wantOk: false,
		},
		{
			name:   "not a fence (fewer tildes)",
			line:   "~~bash",
			wantOk: false,
		},
		{
			name:   "ordinary text",
			line:   "hello world",
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := fenceOpener(tt.line)
			if ok != tt.wantOk {
				t.Fatalf("fenceOpener(%q) ok = %v; want %v", tt.line, ok, tt.wantOk)
			}
			if got != tt.wantMarker {
				t.Errorf("fenceOpener(%q) = %q; want %q", tt.line, got, tt.wantMarker)
			}
		})
	}
}

func TestSplitRuns(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		defaultColumns int
		wantLen        int
		wantRuns       []run
	}{
		{
			name:           "empty body",
			body:           "",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{""}},
			},
		},
		{
			name:           "no directives",
			body:           "line 1\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1", "line 2"}},
			},
		},
		{
			name:           "page break",
			body:           "line 1\n<!-- parchment:page-break -->\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1"}, end: pageBreak},
				{columns: 1, lines: []string{"line 2"}},
			},
		},
		{
			name:           "section break default columns",
			body:           "line 1\n<!-- parchment:section-break -->\nline 2",
			defaultColumns: 2,
			wantRuns: []run{
				{columns: 2, lines: []string{"line 1"}, end: sectionBreak},
				{columns: 2, lines: []string{"line 2"}},
			},
		},
		{
			name:           "section break custom columns",
			body:           "line 1\n<!-- parchment:section-break columns=3 -->\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1"}, end: sectionBreak},
				{columns: 3, lines: []string{"line 2"}},
			},
		},
		{
			name:           "section break continuous",
			body:           "line 1\n<!-- parchment:section-break continuous -->\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1"}, end: sectionBreak, continuous: true},
				{columns: 1, lines: []string{"line 2"}},
			},
		},
		{
			name:           "directive inside fenced block",
			body:           "```markdown\n<!-- parchment:page-break -->\n```\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"```markdown", "<!-- parchment:page-break -->", "```", "line 2"}},
			},
		},
		{
			name:           "windows line endings",
			body:           "line 1\r\n<!-- parchment:page-break -->\r\nline 2",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1"}, end: pageBreak},
				{columns: 1, lines: []string{"line 2"}},
			},
		},
		{
			name:           "multiple directives",
			body:           "part 1\n<!-- parchment:section-break columns=2 continuous -->\npart 2\n<!-- parchment:page-break -->\npart 3",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"part 1"}, end: sectionBreak, continuous: true},
				{columns: 2, lines: []string{"part 2"}, end: pageBreak},
				{columns: 2, lines: []string{"part 3"}}, // columns persists until next section-break
			},
		},
		{
			name:           "trailing directive",
			body:           "line 1\n<!-- parchment:page-break -->",
			defaultColumns: 1,
			wantRuns: []run{
				{columns: 1, lines: []string{"line 1"}, end: pageBreak},
				{columns: 1, lines: nil},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitRuns(tt.body, tt.defaultColumns)
			if len(got) != len(tt.wantRuns) {
				t.Fatalf("splitRuns() returned %d runs; want %d", len(got), len(tt.wantRuns))
			}
			for i := range got {
				if got[i].columns != tt.wantRuns[i].columns {
					t.Errorf("run %d columns = %d; want %d", i, got[i].columns, tt.wantRuns[i].columns)
				}
				if len(got[i].lines) != len(tt.wantRuns[i].lines) {
					t.Errorf("run %d lines length = %d; want %d", i, len(got[i].lines), len(tt.wantRuns[i].lines))
				} else {
					for j := range got[i].lines {
						if got[i].lines[j] != tt.wantRuns[i].lines[j] {
							t.Errorf("run %d line %d = %q; want %q", i, j, got[i].lines[j], tt.wantRuns[i].lines[j])
						}
					}
				}
				if got[i].end != tt.wantRuns[i].end {
					t.Errorf("run %d end = %v; want %v", i, got[i].end, tt.wantRuns[i].end)
				}
				if got[i].continuous != tt.wantRuns[i].continuous {
					t.Errorf("run %d continuous = %v; want %v", i, got[i].continuous, tt.wantRuns[i].continuous)
				}
			}
		})
	}
}
