package chartrender

import (
	"encoding/xml"
	"image/color"
	"strings"
	"testing"

	"github.com/ramjac/parchment/internal/spreadsheet"
)

func TestChartOutputsAndValidation(t *testing.T) {
	data := spreadsheet.ChartData{
		Title: "Sales & <cost>", Type: "bar",
		Labels: []string{"One", "Two"},
		Series: []spreadsheet.ChartSeries{{Name: "Total", Values: []float64{2, -3}}},
	}
	scene, err := Layout(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(SVG(scene), new(any)); err != nil {
		t.Fatalf("invalid SVG: %v", err)
	}
	if !strings.Contains(string(SVG(scene)), "Sales &amp; &lt;cost&gt;") {
		t.Fatal("SVG did not escape the title")
	}
	img := Raster(scene)
	if img.Bounds().Dx() != 720 || img.Bounds().Dy() != 480 {
		t.Fatalf("unexpected raster bounds: %v", img.Bounds())
	}
	if got := color.RGBAModel.Convert(img.At(200, 120)).(color.RGBA); got == (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatal("raster did not draw the positive bar")
	}
	data.Type = "line"
	data.Series = append(data.Series, spreadsheet.ChartSeries{Name: "Other", Values: []float64{1, 4}})
	if _, err := Layout(data); err != nil {
		t.Fatalf("multi-series line chart: %v", err)
	}
	data.Type = "pie"
	if _, err := Layout(data); err == nil {
		t.Fatal("unsupported pie chart accepted")
	}
	data.Type = "bar"
	if _, err := Layout(data); err == nil {
		t.Fatal("multi-series bar chart accepted")
	}
}
