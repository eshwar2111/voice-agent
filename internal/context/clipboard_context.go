package context

import "github.com/atotto/clipboard"

func GetClipboardText() (string, error) {
	return clipboard.ReadAll()
}
