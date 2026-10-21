package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestWrapReviewTextExactMatch(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
	}{
		{
			name:    "empty string",
			content: "",
			width:   80,
		},
		{
			name:    "single line without newline",
			content: "hello world",
			width:   80,
		},
		{
			name:    "single line with trailing newline",
			content: "hello world\n",
			width:   80,
		},
		{
			name:    "multiple lines with trailing newline",
			content: "line 1\nline 2\nline 3\n",
			width:   80,
		},
		{
			name:    "multiple lines without trailing newline",
			content: "line 1\nline 2\nline 3",
			width:   80,
		},
		{
			name:    "consecutive newlines",
			content: "line 1\n\n\nline 2\n\n",
			width:   80,
		},
		{
			name:    "wrapped lines",
			content: "1234567890\nabcdefghij",
			width:   5,
		},
		{
			name:    "wide CJK characters wrapping",
			content: "你好世界学习编程", // each CJK char has runewidth 2 (total width 16)
			width:   6,
		},
		{
			name:    "wide emoji characters wrapping",
			content: "😀😁😂😃😄😅",
			width:   5,
		},
		{
			name:    "mixed ASCII and wide CJK characters",
			content: "Hi你好World世界",
			width:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reference implementation matching original behavior
			var expectedWrapped strings.Builder
			for lineIndex, line := range strings.Split(tt.content, "\n") {
				if lineIndex > 0 {
					expectedWrapped.WriteByte('\n')
				}
				lineWidth := 0
				for _, r := range line {
					runeWidth := max(runewidth.RuneWidth(r), 0)
					if lineWidth > 0 && lineWidth+runeWidth > tt.width {
						expectedWrapped.WriteByte('\n')
						lineWidth = 0
					}
					expectedWrapped.WriteRune(r)
					lineWidth += runeWidth
				}
			}
			expected := expectedWrapped.String()

			got := wrapReviewText(tt.content, tt.width)
			if got != expected {
				t.Errorf("wrapReviewText(%q, %d) = %q, want %q", tt.content, tt.width, got, expected)
			}
		})
	}
}
