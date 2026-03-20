package context

import (
	"time"

	"github.com/atotto/clipboard"
	"github.com/go-vgo/robotgo"
)

func CopySelectedText() (string, error) {
	robotgo.KeyTap("c", "ctrl")
	time.Sleep(200 * time.Millisecond)

	return clipboard.ReadAll()
}
