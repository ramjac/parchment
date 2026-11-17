package presentation

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"example.com/parchment/internal/artifact"
)

var (
	codeDirectiveRE  = regexp.MustCompile(`(?m)^\.code\s+([^\s]+)(?:\s+(.+))?$`)
	imageDirectiveRE = regexp.MustCompile(`(?m)^\.image\s+([^\s]+)(?:\s+([^\s]+))?(?:\s+([^\s]+))?$`)
	hlMarkerRE       = regexp.MustCompile(`\s*//\s*HL.*$`)
)

const maxCodeFileSize = 1024 * 1024 // 1 MiB

// ValidateDirectives checks that .image directives reference embedded images in the presentation
// and that .code directives do not attempt path traversal or use absolute paths.
func ValidateDirectives(p Presentation) error {
	imageMap := make(map[string]bool, len(p.Images))
	for _, img := range p.Images {
		imageMap[img.Name] = true
	}

	for _, match := range imageDirectiveRE.FindAllStringSubmatch(p.Source, -1) {
		name := match[1]
		if !imageMap[name] {
			return fmt.Errorf("presentation image %q not found", name)
		}
	}

	for _, match := range codeDirectiveRE.FindAllStringSubmatch(p.Source, -1) {
		relpath := match[1]
		if filepath.IsAbs(relpath) {
			return fmt.Errorf("code directive path %q must not be absolute", relpath)
		}
		cleaned := filepath.Clean(relpath)
		if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
			return fmt.Errorf("code directive path %q cannot traverse outside parent directory", relpath)
		}
	}

	return nil
}

// ExpandDirectives resolves .image and .code directives within a presentation's slide content.
// baseDir is the presentation file's directory, used to confine .code inclusions.
func ExpandDirectives(source string, baseDir string, images []artifact.Image) (string, error) {
	imageMap := make(map[string]bool, len(images))
	for _, img := range images {
		imageMap[img.Name] = true
	}

	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(source))
	first := true

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !first {
			out.WriteByte('\n')
		}
		first = false

		if match := imageDirectiveRE.FindStringSubmatch(line); match != nil && strings.HasPrefix(trimmed, ".image") {
			name := match[1]
			// Format as standard markdown image tag
			out.WriteString(fmt.Sprintf("![%s](%s)", name, name))
			continue
		}

		if match := codeDirectiveRE.FindStringSubmatch(line); match != nil && strings.HasPrefix(trimmed, ".code") {
			relpath := match[1]
			bounds := ""
			if len(match) > 2 {
				bounds = strings.TrimSpace(match[2])
			}

			codeBlock, err := loadCodeSnippet(baseDir, relpath, bounds)
			if err != nil {
				return "", err
			}
			out.WriteString(codeBlock)
			continue
		}

		out.WriteString(line)
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return out.String(), nil
}

func loadCodeSnippet(baseDir, relpath, bounds string) (string, error) {
	if baseDir == "" {
		return "", errors.New("cannot resolve .code directive without a base directory")
	}

	if filepath.IsAbs(relpath) {
		return "", fmt.Errorf("code directive path %q must not be absolute", relpath)
	}

	cleaned := filepath.Clean(relpath)
	if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("code directive path %q cannot traverse outside parent directory", relpath)
	}

	fullPath := filepath.Join(baseDir, cleaned)

	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("code directive path %q escapes directory", relpath)
	}

	// Symlink escape check
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path %q: %w", relpath, err)
	}

	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		realBase = baseDir
	}

	realRel, err := filepath.Rel(realBase, realPath)
	if err != nil || strings.HasPrefix(realRel, "..") {
		return "", fmt.Errorf("symlink target %q escapes directory", relpath)
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", relpath, err)
	}
	if info.Size() > maxCodeFileSize {
		return "", fmt.Errorf("file %q exceeds maximum allowed size of 1 MiB", relpath)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", relpath, err)
	}

	allLines := strings.Split(string(data), "\n")
	selectedLines, err := filterCodeBounds(allLines, bounds)
	if err != nil {
		return "", err
	}

	// Filter // OMIT and // HL
	var processed []string
	for _, l := range selectedLines {
		t := strings.TrimRight(l, " \t\r")
		if strings.HasSuffix(t, "// OMIT") {
			continue
		}
		// Remove // HL highlighting markers
		l = hlMarkerRE.ReplaceAllString(l, "")
		processed = append(processed, l)
	}

	ext := strings.TrimPrefix(filepath.Ext(relpath), ".")
	var buf bytes.Buffer
	buf.WriteString("```" + ext + "\n")
	buf.WriteString(strings.Join(processed, "\n"))
	if len(processed) > 0 && !strings.HasSuffix(buf.String(), "\n") {
		buf.WriteByte('\n')
	}
	buf.WriteString("```")

	return buf.String(), nil
}

func filterCodeBounds(lines []string, bounds string) ([]string, error) {
	bounds = strings.TrimSpace(bounds)
	if bounds == "" {
		return lines, nil
	}

	// Check line numbers: N or N,M
	if !strings.HasPrefix(bounds, "/") {
		parts := strings.Split(bounds, ",")
		if len(parts) == 1 {
			n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil || n < 1 || n > len(lines) {
				return nil, fmt.Errorf("invalid line number %q", bounds)
			}
			return lines[n-1 : n], nil
		} else if len(parts) == 2 {
			n, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			m, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 != nil || err2 != nil || n < 1 || m < n || m > len(lines) {
				return nil, fmt.Errorf("invalid line range %q", bounds)
			}
			return lines[n-1 : m], nil
		}
		return nil, fmt.Errorf("invalid bounds format %q", bounds)
	}

	// Regex bounds: /regexp/ or /START/,/END/
	if strings.Contains(bounds, "/,/") {
		parts := strings.Split(bounds, "/,/")
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "/") || !strings.HasSuffix(parts[1], "/") {
			return nil, fmt.Errorf("invalid regex bounds format %q", bounds)
		}
		startPattern := strings.TrimPrefix(parts[0], "/")
		endPattern := strings.TrimSuffix(parts[1], "/")

		startRE, err := regexp.Compile(startPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid start regex: %w", err)
		}
		endRE, err := regexp.Compile(endPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid end regex: %w", err)
		}

		startIdx := -1
		for i, line := range lines {
			if startRE.MatchString(line) {
				startIdx = i
				break
			}
		}
		if startIdx == -1 {
			return nil, fmt.Errorf("start pattern %q not found in file", startPattern)
		}

		endIdx := -1
		for i := startIdx; i < len(lines); i++ {
			if endRE.MatchString(lines[i]) {
				endIdx = i
				break
			}
		}
		if endIdx == -1 {
			return nil, fmt.Errorf("end pattern %q not found in file after start line", endPattern)
		}

		return lines[startIdx : endIdx+1], nil
	}

	if strings.HasPrefix(bounds, "/") && strings.HasSuffix(bounds, "/") && len(bounds) >= 2 {
		pattern := bounds[1 : len(bounds)-1]
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regex %q: %w", pattern, err)
		}
		for _, line := range lines {
			if re.MatchString(line) {
				return []string{line}, nil
			}
		}
		return nil, fmt.Errorf("pattern %q not found in file", pattern)
	}

	return nil, fmt.Errorf("unsupported bounds format %q", bounds)
}
