package tui

import (
	"fmt"
	"strings"
	"testing"
)

func makeLargePresentationSource() string {
	var sb strings.Builder
	sb.WriteString("# Large Presentation Title\n\nAuthor Name\n\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "## Slide %d\n\n", i)
		sb.WriteString("Some introductory text for slide.\n\n")
		sb.WriteString("```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```\n\n")
		fmt.Fprintf(&sb, ": Speaker notes for slide %d\n\n", i)
	}
	return sb.String()
}

func BenchmarkPageForSourceLine(b *testing.B) {
	source := makeLargePresentationSource()
	targetLine := 400

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = pageForSourceLine(source, targetLine)
	}
}

func BenchmarkSlideSourceLine(b *testing.B) {
	source := makeLargePresentationSource()
	targetPage := 50

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = slideSourceLine(source, targetPage)
	}
}
