package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var hexUIColor = regexp.MustCompile(`^#(?:[[:xdigit:]]{3}|[[:xdigit:]]{4}|[[:xdigit:]]{6}|[[:xdigit:]]{8})$`)

type uiColorViolation struct {
	position token.Position
	message  string
}

type sourceRange struct {
	start, end token.Pos
}

func TestUIColorsUseSemanticTheme(t *testing.T) {
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch filepath.Base(path) {
			case "theme", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !isProductionTUISource(path) {
			return nil
		}

		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		violations, err := findUIColorViolations(path, source)
		if err != nil {
			return err
		}
		for _, violation := range violations {
			t.Errorf("%s:%d: %s", path, violation.position.Line, violation.message)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isProductionTUISource(path string) bool {
	if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
		return false
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "theme" || part == "testdata" {
			return false
		}
	}
	return true
}

func findUIColorViolations(filename string, source []byte) ([]uiColorViolation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, source, 0)
	if err != nil {
		return nil, err
	}
	lipglossNames, dotImport := lipglossImportNames(file)
	violations := make(map[token.Pos]string)
	adaptiveRanges := make([]sourceRange, 0)

	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.CallExpr:
			if isLipglossSelector(n.Fun, "Color", lipglossNames, dotImport) &&
				len(n.Args) > 0 {
				if literal, ok := n.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
					violations[literal.Pos()] = "use a semantic theme role instead of a string literal passed to lipgloss.Color"
				}
			}
		case *ast.CompositeLit:
			if isLipglossSelector(n.Type, "AdaptiveColor", lipglossNames, dotImport) {
				violations[n.Pos()] = "declare AdaptiveColor palettes only in the shared theme package"
				adaptiveRanges = append(adaptiveRanges, sourceRange{start: n.Pos(), end: n.End()})
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				break
			}
			value, unquoteErr := strconv.Unquote(n.Value)
			if unquoteErr != nil || !hexUIColor.MatchString(value) || insideSourceRange(n.Pos(), adaptiveRanges) {
				break
			}
			if _, isReported := violations[n.Pos()]; !isReported {
				violations[n.Pos()] = "use a semantic theme role instead of a hard-coded hex color"
			}
		}
		return true
	})

	result := make([]uiColorViolation, 0, len(violations))
	for pos, message := range violations {
		result = append(result, uiColorViolation{position: fset.Position(pos), message: message})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].position.Filename != result[j].position.Filename {
			return result[i].position.Filename < result[j].position.Filename
		}
		return result[i].position.Offset < result[j].position.Offset
	})
	return result, nil
}

func lipglossImportNames(file *ast.File) (map[string]bool, bool) {
	names := make(map[string]bool)
	dotImport := false
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath != "github.com/charmbracelet/lipgloss" {
			continue
		}
		if spec.Name == nil {
			names["lipgloss"] = true
			continue
		}
		switch spec.Name.Name {
		case ".":
			dotImport = true
		case "_":
		default:
			names[spec.Name.Name] = true
		}
	}
	return names, dotImport
}

func isLipglossSelector(expr ast.Expr, member string, names map[string]bool, dotImport bool) bool {
	switch selector := expr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := selector.X.(*ast.Ident)
		return ok && names[pkg.Name] && selector.Sel.Name == member
	case *ast.Ident:
		return dotImport && selector.Name == member
	default:
		return false
	}
}

func insideSourceRange(pos token.Pos, ranges []sourceRange) bool {
	for _, source := range ranges {
		if source.start <= pos && pos < source.end {
			return true
		}
	}
	return false
}

func TestFindUIColorViolations(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantMessage string
	}{
		{
			name: "raw hex literal",
			source: `package sample
const accent = "#aabbcc"
`,
			wantMessage: "hard-coded hex color",
		},
		{
			name: "lipgloss color literal",
			source: `package sample
import lipgloss "github.com/charmbracelet/lipgloss"
var accent = lipgloss.Color("red")
`,
			wantMessage: "literal passed to lipgloss.Color",
		},
		{
			name: "aliased adaptive palette",
			source: `package sample
import lg "github.com/charmbracelet/lipgloss"
var accent = lg.AdaptiveColor{Light: "#112233", Dark: "#aabbcc"}
`,
			wantMessage: "AdaptiveColor palettes",
		},
		{
			name: "dot-imported color literal",
			source: `package sample
import . "github.com/charmbracelet/lipgloss"
var accent = Color("#abcdef")
`,
			wantMessage: "literal passed to lipgloss.Color",
		},
		{
			name: "dynamic image color",
			source: `package sample
import lipgloss "github.com/charmbracelet/lipgloss"
func render(imageColor string) {
	_ = lipgloss.NewStyle().Foreground(lipgloss.Color(imageColor))
}
`,
		},
		{
			name: "non-color string",
			source: `package sample
const reference = "#REF!"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findUIColorViolations("sample.go", []byte(tt.source))
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantMessage == "" {
				if len(got) != 0 {
					t.Fatalf("findUIColorViolations() = %#v, want no violations", got)
				}
				return
			}
			for _, violation := range got {
				if strings.Contains(violation.message, tt.wantMessage) {
					return
				}
			}
			t.Fatalf("findUIColorViolations() = %#v, want a violation containing %q", got, tt.wantMessage)
		})
	}
}

func TestIsProductionTUISource(t *testing.T) {
	for _, tt := range []struct {
		path string
		want bool
	}{
		{"model.go", true},
		{"model_test.go", false},
		{"theme/theme.go", false},
		{"testdata/generated.go", false},
		{"termimg/termimg.go", true},
	} {
		if got := isProductionTUISource(tt.path); got != tt.want {
			t.Errorf("isProductionTUISource(%q) = %t, want %t", tt.path, got, tt.want)
		}
	}
}
