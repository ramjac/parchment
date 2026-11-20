// Package chartrender lays out spreadsheet charts for vector and raster output.
package chartrender

import (
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"example.com/parchment/internal/spreadsheet"
)

type Point struct{ X, Y float64 }

type Shape struct {
	Points []Point
	Color  color.RGBA
	Fill   bool
	Width  float64
}

type Text struct {
	X, Y float64
	Text string
}

type Scene struct {
	Width, Height float64
	Shapes        []Shape
	Text          []Text
}

var palette = []color.RGBA{
	{R: 52, G: 101, B: 164, A: 255}, {R: 204, G: 81, B: 62, A: 255},
	{R: 72, G: 145, B: 102, A: 255}, {R: 164, G: 115, B: 45, A: 255},
	{R: 129, G: 91, B: 153, A: 255}, {R: 41, G: 145, B: 152, A: 255},
}

// Layout creates a deterministic, backend-independent chart scene.
func Layout(data spreadsheet.ChartData) (Scene, error) {
	scene := Scene{Width: 720, Height: 480}
	if len(data.Labels) == 0 || len(data.Series) == 0 {
		return scene, fmt.Errorf("chart has no data")
	}
	for _, s := range data.Series {
		if len(s.Values) != len(data.Labels) {
			return scene, fmt.Errorf("chart series dimensions differ")
		}
		for _, v := range s.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return scene, fmt.Errorf("chart data is not finite")
			}
		}
	}
	scene.Text = append(scene.Text, Text{X: 26, Y: 28, Text: cleanLabel(data.Title)})
	black := color.RGBA{A: 255}
	line := func(x1, y1, x2, y2 float64, color color.RGBA) {
		scene.Shapes = append(scene.Shapes, Shape{Points: []Point{{x1, y1}, {x2, y2}}, Color: color, Width: 1.5})
	}
	polygon := func(points []Point, col color.RGBA) {
		scene.Shapes = append(scene.Shapes, Shape{Points: points, Color: col, Fill: true})
	}
	if data.Type != "bar" && data.Type != "line" {
		return scene, fmt.Errorf("unsupported chart type %q", data.Type)
	}
	if data.Type == "bar" && len(data.Series) != 1 {
		return scene, fmt.Errorf("bar charts require exactly one data series")
	}
	minimum, maximum := 0.0, 0.0
	for _, series := range data.Series {
		for _, value := range series.Values {
			minimum, maximum = math.Min(minimum, value), math.Max(maximum, value)
		}
	}
	if minimum == maximum {
		maximum = minimum + 1
	}
	if math.IsInf(maximum-minimum, 0) || maximum == minimum {
		return scene, fmt.Errorf("chart value range is too large")
	}
	const left, right, top, bottom = 70.0, 680.0, 70.0, 390.0
	scale := func(value float64) float64 { return bottom - (value-minimum)/(maximum-minimum)*(bottom-top) }
	zero := scale(0)
	line(left, top, left, bottom, black)
	line(left, zero, right, zero, black)
	scene.Text = append(scene.Text,
		Text{X: 8, Y: top + 5, Text: fmt.Sprintf("%.4g", maximum)},
		Text{X: 8, Y: bottom, Text: fmt.Sprintf("%.4g", minimum)})
	groupWidth := (right - left) / float64(len(data.Labels))
	for i, label := range data.Labels {
		if len(data.Labels) <= 12 {
			scene.Text = append(scene.Text, Text{X: left + float64(i)*groupWidth + 2, Y: 410, Text: cleanLabel(label)})
		}
	}
	for s, series := range data.Series {
		col := palette[s%len(palette)]
		for i, value := range series.Values {
			y := scale(value)
			center := left + (float64(i)+0.5)*groupWidth
			if data.Type == "bar" {
				width := groupWidth * 0.8 / float64(len(data.Series))
				x := left + float64(i)*groupWidth + groupWidth*0.1 + float64(s)*width
				polygon([]Point{{x, zero}, {x + width, zero}, {x + width, y}, {x, y}}, col)
			} else if i > 0 {
				previous := left + (float64(i)-0.5)*groupWidth
				line(previous, scale(series.Values[i-1]), center, y, col)
			} else if len(series.Values) == 1 {
				polygon([]Point{{center - 3, y - 3}, {center + 3, y - 3}, {center + 3, y + 3}, {center - 3, y + 3}}, col)
			}
		}
		if s < 12 {
			x := 70 + float64(s%4)*160
			y := 430 + float64(s/4)*16
			polygon([]Point{{x, y - 9}, {x + 10, y - 9}, {x + 10, y + 1}, {x, y + 1}}, col)
			scene.Text = append(scene.Text, Text{X: x + 15, Y: y, Text: cleanLabel(series.Name)})
		}
	}
	return scene, nil
}

func cleanLabel(text string) string {
	var out strings.Builder
	count := 0
	for _, r := range text {
		if count == 80 {
			break
		}
		if r < 32 || r == 127 || r == 0xfffe || r == 0xffff {
			r = ' '
		}
		out.WriteRune(r)
		count++
	}
	return out.String()
}

// SVG renders a scene as a self-contained SVG image.
func SVG(scene Scene) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`, scene.Width, scene.Height, scene.Width, scene.Height)
	out.WriteString(`<rect width="100%" height="100%" fill="white"/>`)
	for _, shape := range scene.Shapes {
		col := fmt.Sprintf("#%02x%02x%02x", shape.Color.R, shape.Color.G, shape.Color.B)
		points := make([]string, len(shape.Points))
		for i, p := range shape.Points {
			points[i] = fmt.Sprintf("%.3f,%.3f", p.X, p.Y)
		}
		if shape.Fill {
			fmt.Fprintf(&out, `<polygon points="%s" fill="%s"/>`, strings.Join(points, " "), col)
		} else {
			fmt.Fprintf(&out, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.2f"/>`, strings.Join(points, " "), col, shape.Width)
		}
	}
	for _, text := range scene.Text {
		fmt.Fprintf(&out, `<text x="%.3f" y="%.3f" font-family="sans-serif" font-size="13">%s</text>`, text.X, text.Y, html.EscapeString(text.Text))
	}
	out.WriteString(`</svg>`)
	return []byte(out.String())
}

// Raster draws the scene into pixels for terminal graphics protocols.
func Raster(scene Scene) image.Image {
	width, height := int(scene.Width), int(scene.Height)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	for _, shape := range scene.Shapes {
		if len(shape.Points) < 2 {
			continue
		}
		points := shape.Points
		if !shape.Fill {
			a, b := points[0], points[1]
			dx, dy := b.X-a.X, b.Y-a.Y
			length := math.Hypot(dx, dy)
			if length == 0 {
				continue
			}
			x, y := -dy/length*shape.Width/2, dx/length*shape.Width/2
			points = []Point{{a.X + x, a.Y + y}, {b.X + x, b.Y + y}, {b.X - x, b.Y - y}, {a.X - x, a.Y - y}}
		}
		minX, minY, maxX, maxY := points[0].X, points[0].Y, points[0].X, points[0].Y
		for _, point := range points[1:] {
			minX, minY = math.Min(minX, point.X), math.Min(minY, point.Y)
			maxX, maxY = math.Max(maxX, point.X), math.Max(maxY, point.Y)
		}
		bounds := image.Rect(max(0, int(math.Floor(minX))-1), max(0, int(math.Floor(minY))-1),
			min(width, int(math.Ceil(maxX))+1), min(height, int(math.Ceil(maxY))+1))
		if bounds.Empty() {
			continue
		}
		raster := vector.NewRasterizer(bounds.Dx(), bounds.Dy())
		raster.MoveTo(float32(points[0].X-float64(bounds.Min.X)), float32(points[0].Y-float64(bounds.Min.Y)))
		for _, point := range points[1:] {
			raster.LineTo(float32(point.X-float64(bounds.Min.X)), float32(point.Y-float64(bounds.Min.Y)))
		}
		raster.ClosePath()
		raster.Draw(img, bounds, image.NewUniform(shape.Color), image.Point{})
	}
	drawer := font.Drawer{Dst: img, Src: image.Black, Face: basicfont.Face7x13}
	for _, text := range scene.Text {
		drawer.Dot = fixed.P(int(text.X), int(text.Y))
		drawer.DrawString(text.Text)
	}
	return img
}
