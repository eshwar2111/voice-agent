package vision

import (
	"bytes"
	"image/png"
	"time"

	"github.com/kbinani/screenshot"
)

type ScreenFrame struct {
	Image []byte
	Time  time.Time
}

func CaptureSequence(frames int) ([]ScreenFrame, error) {
	var result []ScreenFrame

	for i := 0; i < frames; i++ {
		img, err := screenshot.CaptureDisplay(0)
		if err != nil {
			return nil, err
		}

		buf := new(bytes.Buffer)
		png.Encode(buf, img)

		result = append(result, ScreenFrame{
			Image: buf.Bytes(),
			Time:  time.Now(),
		})

		time.Sleep(500 * time.Millisecond)
	}

	return result, nil
}
