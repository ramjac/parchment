package document

import (
	"testing"
)

func BenchmarkWithImage(b *testing.B) {
	d := Document{
		Images: []Image{},
	}
	for i := 0; i < 100; i++ {
		d.Images = append(d.Images, Image{Name: "image" + string(rune(i))})
	}

	img := Image{Name: "image50", Data: []byte("newdata")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = WithImage(d, img)
	}
}
