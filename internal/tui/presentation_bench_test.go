package tui

import (
	"fmt"
	"strings"
	"testing"
)

func generateBenchmarkPresentationSource(slidesCount int, linesPerSlide int) string {
	var b strings.Builder
	b.WriteString("# Presentation Title\n\nIntro line 1\nIntro line 2\n\n")
	for i := 1; i <= slidesCount; i++ {
		fmt.Fprintf(&b, "## Slide %d\n", i)
		b.WriteString("Some text before code block\n```go\nfunc hello() {\n  fmt.Println(\"## fake slide header in fence\")\n}\n```\n")
		for j := 0; j < linesPerSlide; j++ {
			fmt.Fprintf(&b, "Line %d of content in slide %d\n", j, i)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func BenchmarkPageForSourceLine(b *testing.B) {
	smallSource := generateBenchmarkPresentationSource(5, 10)   // ~100 lines
	mediumSource := generateBenchmarkPresentationSource(20, 30) // ~800 lines
	largeSource := generateBenchmarkPresentationSource(100, 50) // ~6000 lines

	b.Run("Small-Line10", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = pageForSourceLine(smallSource, 10)
		}
	})

	b.Run("Small-Line80", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = pageForSourceLine(smallSource, 80)
		}
	})

	b.Run("Medium-Line400", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = pageForSourceLine(mediumSource, 400)
		}
	})

	b.Run("Large-Line3000", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = pageForSourceLine(largeSource, 3000)
		}
	})
}

func BenchmarkSlideSourceLine(b *testing.B) {
	mediumSource := generateBenchmarkPresentationSource(20, 30)

	b.Run("Medium-Page10", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = slideSourceLine(mediumSource, 10)
		}
	})
}
