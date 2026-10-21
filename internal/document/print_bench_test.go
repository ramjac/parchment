package document

import "testing"

func BenchmarkInlineText_Plain(b *testing.B) {
	text := "This is a plain paragraph of text with no markdown formatting whatsoever."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = inlineText(text)
	}
}

func BenchmarkInlineText_Formatted(b *testing.B) {
	text := "Some **bold** and *italic* text with a [link](https://example.com) and `code` and ![image](img.png) and ~~strike~~ and __under__."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = inlineText(text)
	}
}

func BenchmarkInlineText_Mixed(b *testing.B) {
	texts := []string{
		"This is plain text without any markup.",
		"Here is **bold** text.",
		"Another plain line in the document.",
		"A line with a [link](https://example.com) inside.",
		"Just more plain text for testing.",
		"Some `code` snippet here.",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, t := range texts {
			_ = inlineText(t)
		}
	}
}

func BenchmarkWrap_ShortText(b *testing.B) {
	text := "This is a simple sentence that needs to be word wrapped across multiple lines."
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrap(text, 40, "", "")
	}
}

func BenchmarkWrap_LongText(b *testing.B) {
	text := "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum. "
	text = text + text + text + text // ~1.8KB of text
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrap(text, 80, "> ", "> ")
	}
}

func BenchmarkPaginate(b *testing.B) {
	body := `# Title

Some **bold** and *italic* text with a [link](https://example.com).

- first item that is long enough to wrap around the narrow measure of this printed page okay
- second item with ` + "`code`" + ` and ~~strike~~

![A chart](image-0123456789abcdef.png)

> quoted text here with __underlined__ words

This is a regular paragraph without any special formatting characters at all.
Another standard paragraph containing normal text to represent typical document content.
`
	d := Document{Body: body, Layout: DefaultLayout()}
	d.Title = "Benchmark Doc"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Paginate(d)
	}
}
