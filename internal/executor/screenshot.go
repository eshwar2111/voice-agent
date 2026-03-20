package executor

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	"github.com/kbinani/screenshot"
)

// CaptureScreen takes a screenshot of the primary display and converts it to a JPEG byte array.
// It downscales to max 1280px width for faster LLM upload while preserving readability.
func CaptureScreen() ([]byte, error) {
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, fmt.Errorf("failed to capture screen: %v", err)
	}

	// Downscale if wider than 1280px — LLMs don't need full-res screenshots
	var encoded image.Image = img
	const maxWidth = 1280
	if img.Bounds().Dx() > maxWidth {
		encoded = downscaleFast(img, maxWidth)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, encoded, &jpeg.Options{Quality: 60}); err != nil {
		return nil, fmt.Errorf("failed to encode screenshot: %v", err)
	}

	return buf.Bytes(), nil
}

// downscaleFast resizes an image using nearest-neighbor with direct pixel access.
// Much faster than the previous version that called src.At() per pixel.
func downscaleFast(src image.Image, targetWidth int) *image.RGBA {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	targetH := targetWidth * srcH / srcW

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetH))

	// If source is already RGBA, use direct pixel access (fast path)
	if rgba, ok := src.(*image.RGBA); ok {
		for y := 0; y < targetH; y++ {
			srcY := y * srcH / targetH
			srcRowOffset := (srcY + srcBounds.Min.Y) * rgba.Stride
			dstRowOffset := y * dst.Stride
			for x := 0; x < targetWidth; x++ {
				srcX := x * srcW / targetWidth
				srcOffset := srcRowOffset + (srcX+srcBounds.Min.X)*4
				dstOffset := dstRowOffset + x*4
				copy(dst.Pix[dstOffset:dstOffset+4], rgba.Pix[srcOffset:srcOffset+4])
			}
		}
		return dst
	}

	// Generic fallback: convert source to RGBA first, then scale
	tmp := image.NewRGBA(srcBounds)
	draw.Draw(tmp, srcBounds, src, srcBounds.Min, draw.Src)
	return downscaleFast(tmp, targetWidth)
}

// downscaleBilinear is a higher-quality downscale using bilinear interpolation.
// Not used by default but available if quality matters more than speed.
func downscaleBilinear(src image.Image, targetWidth int) *image.RGBA {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	targetH := targetWidth * srcH / srcW

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetH))

	xRatio := float64(srcW) / float64(targetWidth)
	yRatio := float64(srcH) / float64(targetH)

	for y := 0; y < targetH; y++ {
		srcY := float64(y) * yRatio
		y0 := int(srcY)
		y1 := y0 + 1
		if y1 >= srcH {
			y1 = srcH - 1
		}
		yFrac := srcY - float64(y0)

		for x := 0; x < targetWidth; x++ {
			srcX := float64(x) * xRatio
			x0 := int(srcX)
			x1 := x0 + 1
			if x1 >= srcW {
				x1 = srcW - 1
			}
			xFrac := srcX - float64(x0)

			c00 := color.NRGBAModel.Convert(src.At(x0+srcBounds.Min.X, y0+srcBounds.Min.Y)).(color.NRGBA)
			c10 := color.NRGBAModel.Convert(src.At(x1+srcBounds.Min.X, y0+srcBounds.Min.Y)).(color.NRGBA)
			c01 := color.NRGBAModel.Convert(src.At(x0+srcBounds.Min.X, y1+srcBounds.Min.Y)).(color.NRGBA)
			c11 := color.NRGBAModel.Convert(src.At(x1+srcBounds.Min.X, y1+srcBounds.Min.Y)).(color.NRGBA)

			lerp := func(a, b uint8, t float64) uint8 {
				return uint8(float64(a)*(1-t) + float64(b)*t)
			}

			top := lerp(c00.R, c10.R, xFrac)
			bot := lerp(c01.R, c11.R, xFrac)
			r := lerp(top, bot, yFrac)

			top = lerp(c00.G, c10.G, xFrac)
			bot = lerp(c01.G, c11.G, xFrac)
			g := lerp(top, bot, yFrac)

			top = lerp(c00.B, c10.B, xFrac)
			bot = lerp(c01.B, c11.B, xFrac)
			b := lerp(top, bot, yFrac)

			dst.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return dst
}
