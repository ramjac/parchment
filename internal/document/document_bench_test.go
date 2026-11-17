package document

import (
	"testing"

	"example.com/parchment/internal/artifact"
)

func BenchmarkWithImage(b *testing.B) {
	d := Document{
		Images: []artifact.Image{},
	}
	for i := 0; i < 100; i++ {
		d.Images = append(d.Images, artifact.Image{Name: "image" + string(rune(i))})
	}

	img := artifact.Image{Name: "image50", Data: []byte("newdata")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = WithImage(d, img)
	}
}
