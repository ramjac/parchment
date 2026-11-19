package termimg

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

const (
	sixelCellPixelWidth  = 8
	sixelCellPixelHeight = 16
	sixelPaletteLevels   = 6
	sixelPaletteSize     = sixelPaletteLevels * sixelPaletteLevels * sixelPaletteLevels
	sixelTransparent     = 255
)

func encodeSixel(img image.Image, width, height int) string {
	bounds := img.Bounds()
	pixels := make([]uint8, width*height)
	var used [sixelPaletteSize]bool
	for y := 0; y < height; y++ {
		srcY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			srcX := bounds.Min.X + x*bounds.Dx()/width
			rgba := color.NRGBAModel.Convert(img.At(srcX, srcY)).(color.NRGBA)
			if rgba.A == 0 {
				pixels[y*width+x] = sixelTransparent
				continue
			}
			r, g, b := quantizeSixelChannel(rgba.R), quantizeSixelChannel(rgba.G), quantizeSixelChannel(rgba.B)
			index := r*sixelPaletteLevels*sixelPaletteLevels + g*sixelPaletteLevels + b
			pixels[y*width+x] = uint8(index)
			used[index] = true
		}
	}

	var registers [sixelPaletteSize]int
	register := 0
	var out strings.Builder
	fmt.Fprintf(&out, "\x1bP0;1;0q\"1;1;%d;%d", width, height)
	for index, present := range used {
		if !present {
			continue
		}
		registers[index] = register
		r := index / (sixelPaletteLevels * sixelPaletteLevels)
		g := index / sixelPaletteLevels % sixelPaletteLevels
		b := index % sixelPaletteLevels
		fmt.Fprintf(&out, "#%d;2;%d;%d;%d", register, r*20, g*20, b*20)
		register++
	}

	for y := 0; y < height; y += 6 {
		wrotePlane := false
		for index, present := range used {
			if !present {
				continue
			}
			masks := make([]byte, width)
			hasPixel := false
			for x := 0; x < width; x++ {
				for dy := 0; dy < 6 && y+dy < height; dy++ {
					if pixels[(y+dy)*width+x] == uint8(index) {
						masks[x] |= 1 << dy
						hasPixel = true
					}
				}
			}
			if !hasPixel {
				continue
			}
			if wrotePlane {
				out.WriteByte('$')
			}
			fmt.Fprintf(&out, "#%d", registers[index])
			writeSixelRuns(&out, masks)
			wrotePlane = true
		}
		out.WriteByte('-')
	}
	out.WriteString("\x1b\\")
	return out.String()
}

func quantizeSixelChannel(channel uint8) int {
	return min((int(channel)+25)/51, sixelPaletteLevels-1)
}

func writeSixelRuns(out *strings.Builder, masks []byte) {
	for start := 0; start < len(masks); {
		end := start + 1
		for end < len(masks) && masks[end] == masks[start] {
			end++
		}
		count := end - start
		character := rune(masks[start] + 63)
		if count >= 4 {
			fmt.Fprintf(out, "!%d%c", count, character)
		} else {
			for range count {
				out.WriteRune(character)
			}
		}
		start = end
	}
}
