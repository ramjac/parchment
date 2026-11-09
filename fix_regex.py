import re

with open("internal/document/print.go", "r") as f:
    text = f.read()

# 1. Lift slug regex out
slug_re = """	anchorRE    = regexp.MustCompile(`(?i)<a\\s+(?:id|name)=["']([^"']+)["'][^>]*>`)
	slugPunctRE = regexp.MustCompile(`[^a-z0-9\\s-]+`)
	slugSpaceRE = regexp.MustCompile(`\\s+`)
)"""
text = text.replace('	anchorRE    = regexp.MustCompile(`(?i)<a\\s+(?:id|name)=["\']([^"\']+)["\'][^>]*>`)\n)', slug_re)

# 2. Update slugify function
slugify_func = """// slugify converts a heading title into a GitHub-compatible anchor slug.
func slugify(title string, seen map[string]int) string {
	title = strings.ToLower(title)
	title = slugPunctRE.ReplaceAllString(title, "")
	title = slugSpaceRE.ReplaceAllString(strings.TrimSpace(title), "-")

	count := seen[title]
	seen[title] = count + 1
	if count > 0 {
		return fmt.Sprintf("%s-%d", title, count)
	}
	return title
}"""
text = re.sub(
    r"// slugify converts a heading title into a GitHub-compatible anchor slug\.\nfunc slugify\(title string, seen map\[string\]int\) string \{[\s\S]+?return title\n\}",
    slugify_func,
    text
)

with open("internal/document/print.go", "w") as f:
    f.write(text)
