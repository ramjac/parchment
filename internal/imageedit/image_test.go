package imageedit

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
)

func sampleImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(y*3 + x + 1), A: 255})
		}
	}
	return img
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCropAndResize(t *testing.T) {
	img, err := Decode(pngBytes(t, sampleImage()))
	if err != nil {
		t.Fatal(err)
	}
	cropped, err := img.Crop(1, 0, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if cropped.Bounds() != image.Rect(0, 0, 2, 2) ||
		color.NRGBAModel.Convert(cropped.At(0, 1)).(color.NRGBA).R != 5 {
		t.Fatal("crop did not preserve the requested pixels")
	}
	for _, rect := range [][4]int{
		{-1, 0, 1, 1}, {0, -1, 1, 1}, {0, 0, 0, 1}, {2, 0, 2, 1},
		{0, 2, 1, 1}, {math.MaxInt, 0, 2, 1}, {0, 0, math.MaxInt, 1},
	} {
		if _, err := img.Crop(rect[0], rect[1], rect[2], rect[3]); err == nil {
			t.Errorf("accepted invalid crop %v", rect)
		}
	}
	for _, tt := range []struct {
		w, h, wantW, wantH int
	}{
		{6, 0, 6, 4}, {0, 3, 5, 3}, {1, 0, 1, 1}, {0, 1, 2, 1}, {4, 7, 4, 7},
	} {
		resized, err := img.Resize(tt.w, tt.h)
		if err != nil || resized.Bounds().Size() != image.Pt(tt.wantW, tt.wantH) {
			t.Fatalf("resize %dx%d: %v, %v", tt.w, tt.h, resized, err)
		}
	}
	for _, size := range [][2]int{{0, 0}, {-1, 2}, {2, -1}, {16385, 1}, {10001, 10000}, {math.MaxInt, 1}} {
		if _, err := img.Resize(size[0], size[1]); err == nil {
			t.Errorf("accepted invalid resize %v", size)
		}
	}
	if img.Bounds() != image.Rect(0, 0, 3, 2) ||
		color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA).R != 1 {
		t.Fatal("editing mutated the source")
	}
}

func TestImageEncoding(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	img, err := Decode(pngBytes(t, src))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"png", "jpeg"} {
		data, err := img.Encode(format, DefaultQuality)
		if err != nil {
			t.Fatal(err)
		}
		result, err := Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, a := result.At(2, 2).RGBA()
		if format == "png" && a != 0 {
			t.Fatal("PNG lost transparency")
		}
		if format == "jpeg" && (r < 65000 || g < 65000 || b < 65000 || a != 65535) {
			t.Fatalf("JPEG matte is not white: %d %d %d %d", r, g, b, a)
		}
	}
	half := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			half.SetNRGBA(x, y, color.NRGBA{R: 255, A: 128})
		}
	}
	mixed, _ := Decode(pngBytes(t, half))
	data, _ := mixed.Encode("jpeg", 100)
	out, _ := Decode(data)
	c := color.NRGBAModel.Convert(out.At(2, 2)).(color.NRGBA)
	if c.R < 250 || c.G < 123 || c.G > 131 || c.B < 123 || c.B > 131 {
		t.Fatalf("incorrect alpha compositing: %v", c)
	}
	for _, quality := range []int{0, -1, 101} {
		if _, err := img.Encode("jpeg", quality); err == nil {
			t.Errorf("accepted quality %d", quality)
		}
	}
	if _, err := img.Encode("gif", 90); err == nil {
		t.Fatal("accepted GIF output")
	}
}

func TestDecodeRejectsUnsupportedAndOversizedImages(t *testing.T) {
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, sampleImage(), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("not an image"), gifData.Bytes(), pngBytes(t, sampleImage())[:40]} {
		if _, err := Decode(data); err == nil {
			t.Fatal("accepted invalid, truncated, or unsupported image")
		}
	}
	for _, size := range [][2]int{{16385, 1}, {10001, 10000}} {
		data := pngBytes(t, sampleImage())
		binary.BigEndian.PutUint32(data[16:20], uint32(size[0]))
		binary.BigEndian.PutUint32(data[20:24], uint32(size[1]))
		binary.BigEndian.PutUint32(data[29:33], crc32.ChecksumIEEE(data[12:29]))
		if _, err := Decode(data); err == nil {
			t.Fatalf("accepted dimensions %v", size)
		}
	}
}

func exifData(order binary.ByteOrder, orientation uint16) []byte {
	data := make([]byte, 26)
	copy(data, "II")
	if order == binary.BigEndian {
		copy(data, "MM")
	}
	order.PutUint16(data[2:4], 42)
	order.PutUint32(data[4:8], 8)
	order.PutUint16(data[8:10], 1)
	order.PutUint16(data[10:12], 0x0112)
	order.PutUint16(data[12:14], 3)
	order.PutUint32(data[14:18], 1)
	order.PutUint16(data[18:20], orientation)
	return data
}

func withAPP1(jpegData, payload []byte) []byte {
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	result := append([]byte{}, jpegData[:2]...)
	result = append(result, segment...)
	result = append(result, payload...)
	return append(result, jpegData[2:]...)
}

func TestEXIFOrientations(t *testing.T) {
	wantPixels := [][]uint8{
		{1, 2, 3, 4, 5, 6},
		{3, 2, 1, 6, 5, 4},
		{6, 5, 4, 3, 2, 1},
		{4, 5, 6, 1, 2, 3},
		{1, 4, 2, 5, 3, 6},
		{4, 1, 5, 2, 6, 3},
		{6, 3, 5, 2, 4, 1},
		{3, 6, 2, 5, 1, 4},
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, sampleImage(), &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	baseline, err := Decode(jpegBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 8; n++ {
		pixels := orient(sampleImage(), n)
		var got []uint8
		for y := 0; y < pixels.Bounds().Dy(); y++ {
			for x := 0; x < pixels.Bounds().Dx(); x++ {
				got = append(got, color.NRGBAModel.Convert(pixels.At(x, y)).(color.NRGBA).R)
			}
		}
		if !bytes.Equal(got, wantPixels[n-1]) {
			t.Fatalf("orientation %d: %v, want %v", n, got, wantPixels[n-1])
		}
		for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
			data := withAPP1(jpegBuf.Bytes(), append([]byte("Exif\x00\x00"), exifData(order, uint16(n))...))
			img, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			expected := orient(baseline.pixels, n)
			if img.Bounds() != expected.Bounds() {
				t.Fatalf("orientation %d dimensions: %v", n, img.Bounds())
			}
			for y := 0; y < expected.Bounds().Dy(); y++ {
				for x := 0; x < expected.Bounds().Dx(); x++ {
					if img.At(x, y) != expected.At(x, y) {
						t.Fatalf("orientation %d pixel %d,%d", n, x, y)
					}
				}
			}
			cropped, err := img.Crop(0, 0, 1, 1)
			if err != nil || cropped.At(0, 0) != color.NRGBAModel.Convert(expected.At(0, 0)) {
				t.Fatalf("crop after orientation %d: %v", n, err)
			}
			encoded, err := img.Encode("jpeg", 90)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("Exif\x00\x00")) {
				t.Fatal("output retained EXIF metadata")
			}
		}
	}
	if _, err := Decode(withAPP1(jpegBuf.Bytes(), []byte("http://ns.adobe.com/xap/1.0/"))); err != nil {
		t.Fatalf("unrelated APP1 metadata: %v", err)
	}
}

func TestMalformedEXIF(t *testing.T) {
	for _, change := range []func([]byte){
		func(b []byte) { b[0] = 'X' },
		func(b []byte) { b[2] = 0 },
		func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], math.MaxUint32) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[8:10], 65535) },
		func(b []byte) { b[12] = 4 },
		func(b []byte) { b[14] = 2 },
		func(b []byte) { b[18] = 0 },
		func(b []byte) { b[18] = 9 },
	} {
		data := exifData(binary.LittleEndian, 1)
		change(data)
		if _, _, err := exifOrientation(data); err == nil {
			t.Fatalf("accepted malformed EXIF %x", data)
		}
	}
	data := exifData(binary.LittleEndian, 1)
	for n := 0; n < len(data); n++ {
		if _, _, err := exifOrientation(data[:n]); err == nil {
			t.Errorf("accepted truncated EXIF at %d", n)
		}
	}
	data[10] = 0
	if _, present, err := exifOrientation(data); err != nil || present {
		t.Fatalf("absent orientation: present=%v, err=%v", present, err)
	}
}

func FuzzEXIFOrientation(f *testing.F) {
	f.Add(exifData(binary.LittleEndian, 6))
	f.Add(exifData(binary.BigEndian, 8))
	f.Add([]byte("Exif"))
	f.Fuzz(func(t *testing.T, data []byte) {
		value, present, err := exifOrientation(data)
		if err == nil && present && (value < 1 || value > 8) {
			t.Fatalf("invalid successful orientation %d", value)
		}
		_, _ = jpegOrientation(data)
	})
}

type fakeRepository struct {
	data  []byte
	saved []byte
}

func (r *fakeRepository) ReadImage(context.Context, string) ([]byte, any, error) {
	return r.data, "source", nil
}
func (r *fakeRepository) PrepareImageWrite(context.Context, any, string, bool, bool) (any, error) {
	return "target", nil
}
func (r *fakeRepository) WriteImage(_ context.Context, _, _ any, data []byte) error {
	r.saved = data
	return nil
}

func (r *fakeRepository) WriteImageSnapshot(ctx context.Context, src, dst any, data []byte) (any, error) {
	return "saved", r.WriteImage(ctx, src, dst, data)
}

func TestServiceSave(t *testing.T) {
	repo := &fakeRepository{data: pngBytes(t, sampleImage())}
	service := NewService(repo)
	ctx := context.Background()
	img, err := service.Load(ctx, "input.bin")
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.PrepareSave(ctx, img, "output.JPEG", false, false)
	if err != nil {
		t.Fatal(err)
	}
	img, _ = img.Resize(6, 0)
	if err := service.Save(ctx, img, target, 90); err != nil {
		t.Fatal(err)
	}
	saved, err := Decode(repo.saved)
	if err != nil || saved.Format() != "jpeg" || saved.Bounds().Size() != image.Pt(6, 4) {
		t.Fatalf("saved image %v, %v", saved, err)
	}
	if _, err := service.PrepareSave(ctx, img, "out.gif", false, false); err == nil {
		t.Fatal("accepted unsupported output extension")
	}
	inPlace, err := service.PrepareSave(ctx, img, "", true, false)
	if err != nil || inPlace.format != "png" {
		t.Fatalf("in-place format: %v, %v", inPlace, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.Save(canceled, img, target, 90); err == nil {
		t.Fatal("ignored cancellation")
	}
}
