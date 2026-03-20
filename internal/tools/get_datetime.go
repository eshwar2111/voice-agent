package tools

import (
	"context"
	"encoding/json"
	"time"
)

type DatetimeTool struct{}

func (d *DatetimeTool) Name() string {
	return "get_datetime"
}

func (d *DatetimeTool) Description() string {
	return "Returns the current date and time."
}

func (d *DatetimeTool) Parameters() string {
	return `{}`
}

func (d *DatetimeTool) RequiresConfirmation() bool {
	return false
}

func (d *DatetimeTool) Execute(ctx context.Context, rawParams json.RawMessage) (string, error) {
	return time.Now().Format(time.RFC1123), nil
}
