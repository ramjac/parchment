package editor

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

	"example.com/parchment/internal/tui/theme"
)

type markdownRole uint8

const (
	markdownPlain markdownRole = iota
	markdownH1
	markdownH2
	markdownH3
	markdownH4
	markdownH5
	markdownH6
	markdownLink
	markdownImage
	markdownCode
	markdownQuote
	markdownList
	markdownEnumeration
	markdownRule
	markdownCodeBlock
	markdownRoleCount
)

type markdownPalette [markdownRoleCount]lipgloss.Style

func editorMarkdownPalette(t theme.Theme) markdownPalette {
	var palette markdownPalette
	for role, color := range map[markdownRole]*string{
		markdownH1:          t.Markdown.H1.Color,
		markdownH2:          t.Markdown.H2.Color,
		markdownH3:          t.Markdown.H3.Color,
		markdownH4:          t.Markdown.H4.Color,
		markdownH5:          t.Markdown.H5.Color,
		markdownH6:          t.Markdown.H6.Color,
		markdownLink:        t.Markdown.LinkText.Color,
		markdownImage:       t.Markdown.ImageText.Color,
		markdownCode:        t.Markdown.Code.Color,
		markdownQuote:       t.Markdown.BlockQuote.Color,
		markdownList:        t.Markdown.Item.Color,
		markdownEnumeration: t.Markdown.Enumeration.Color,
		markdownRule:        t.Markdown.HorizontalRule.Color,
		markdownCodeBlock:   t.Markdown.CodeBlock.Color,
	} {
		if color != nil {
			palette[role] = lipgloss.NewStyle().Foreground(lipgloss.Color(*color))
		}
	}
	return palette
}

type markdownFence struct {
	char       rune
	count      int
	quoteDepth int
}

// fenceLine recognizes Markdown fences outside indented code blocks.
func fenceLine(line []rune) (rune, int, int, bool) {
	quotes := 0
	for {
		i := 0
		for i < len(line) && i < 3 && line[i] == ' ' {
			i++
		}
		if i >= len(line) || line[i] != '>' {
			break
		}
		quotes++
		line = line[i+1:]
		if len(line) > 0 && line[0] == ' ' {
			line = line[1:]
		}
	}
	i := 0
	for i < len(line) && i < 4 && line[i] == ' ' {
		i++
	}
	if i >= 4 || i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, quotes, false
	}
	start, char := i, line[i]
	for i < len(line) && line[i] == char {
		i++
	}
	if i-start < 3 || (char == '`' && strings.ContainsRune(string(line[i:]), '`')) {
		return 0, 0, quotes, false
	}
	return char, i - start, quotes, strings.TrimSpace(string(line[i:])) == ""
}

func (f *markdownFence) consume(line []rune) bool {
	char, count, quotes, closing := fenceLine(line)
	if f.char != 0 && quotes < f.quoteDepth {
		*f = markdownFence{}
	}
	if f.char != 0 {
		if char == f.char && quotes == f.quoteDepth && count >= f.count && closing {
			*f = markdownFence{}
		}
		return true
	}
	if char != 0 {
		*f = markdownFence{char: char, count: count, quoteDepth: quotes}
		return true
	}
	return false
}

func markdownLineRoles(line, next []rune, fence *markdownFence) []markdownRole {
	roles := make([]markdownRole, len(line))
	fill := func(start, end int, role markdownRole) {
		for i := start; i < end; i++ {
			roles[i] = role
		}
	}
	if fence.consume(line) {
		fill(0, len(line), markdownCodeBlock)
		return roles
	}
	start := 0
	for start < len(line) && line[start] == ' ' {
		start++
	}
	if start >= 4 || start == len(line) {
		return roles
	}
	content := start
	for content < len(line) && line[content] == '>' {
		fill(0, len(line), markdownQuote)
		content++
		if content < len(line) && line[content] == ' ' {
			content++
		}
	}
	end := content
	for end < len(line) && line[end] == '#' {
		end++
	}
	if end > content && end-content <= 6 && (end == len(line) || unicode.IsSpace(line[end])) {
		fill(content, len(line), markdownH1+markdownRole(end-content-1))
	} else if level := setextUnderline(next); level > 0 {
		fill(content, len(line), markdownH1+markdownRole(level-1))
	} else if horizontalRule(line[content:]) {
		fill(content, len(line), markdownRule)
	} else if end = listMarkerEnd(line, content); end > content {
		role := markdownList
		if line[content] >= '0' && line[content] <= '9' {
			role = markdownEnumeration
		}
		fill(content, end, role)
	}

	for i := content; i < len(line); {
		if line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == '`' {
			end := i + 1
			for end < len(line) && line[end] == '`' {
				end++
			}
			marker := string(line[i:end])
			if close := strings.Index(string(line[end:]), marker); close >= 0 {
				closeEnd := end + len([]rune(string(line[end:])[:close])) + len([]rune(marker))
				fill(i, closeEnd, markdownCode)
				i = closeEnd
				continue
			}
			i = end
			continue
		}
		linkStart, role := i, markdownLink
		if line[i] == '!' && i+1 < len(line) && line[i+1] == '[' {
			linkStart, role = i+1, markdownImage
		}
		if line[linkStart] == '[' {
			close := linkStart + 1
			for close < len(line) && line[close] != ']' {
				if line[close] == '\\' && close+1 < len(line) {
					close++
				}
				close++
			}
			if close == len(line) {
				break
			}
			if close+1 < len(line) && (line[close+1] == '(' || line[close+1] == '[') {
				terminator := ')'
				if line[close+1] == '[' {
					terminator = ']'
				}
				end := close + 2
				for end < len(line) && line[end] != terminator {
					if line[end] == '\\' && end+1 < len(line) {
						end++
					}
					end++
				}
				if end < len(line) {
					fill(i, end+1, role)
					i = end + 1
					continue
				}
				i = close + 2
				continue
			}
			i = close + 1
			continue
		}
		i++
	}
	return roles
}

func setextUnderline(line []rune) int {
	s := strings.TrimSpace(string(line))
	if s == "" || (s[0] != '=' && s[0] != '-') {
		return 0
	}
	if strings.Trim(s, s[:1]) != "" {
		return 0
	}
	if s[0] == '=' {
		return 1
	}
	return 2
}

func horizontalRule(line []rune) bool {
	marker, count := rune(0), 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			continue
		}
		if r != '*' && r != '-' && r != '_' || marker != 0 && marker != r {
			return false
		}
		marker = r
		count++
	}
	return count >= 3
}

func listMarkerEnd(line []rune, start int) int {
	if start >= len(line) {
		return start
	}
	end := start
	if line[end] == '-' || line[end] == '+' || line[end] == '*' {
		end++
	} else {
		for end < len(line) && line[end] >= '0' && line[end] <= '9' && end-start < 9 {
			end++
		}
		if end == start || end >= len(line) || (line[end] != '.' && line[end] != ')') {
			return start
		}
		end++
	}
	if end == len(line) || unicode.IsSpace(line[end]) {
		return end
	}
	return start
}
