package automation

import (
	"bytes"
	"fmt"
	"image/png"

	"github.com/kbinani/screenshot"
)

// CaptureScreen takes a screenshot of the primary display and returns it as PNG bytes.
func CaptureScreen() ([]byte, error) {
	if screenshot.NumActiveDisplays() == 0 {
		return nil, fmt.Errorf("no active displays found")
	}

	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return nil, fmt.Errorf("failed to capture screen: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode screenshot to png: %w", err)
	}

	return buf.Bytes(), nil
}
