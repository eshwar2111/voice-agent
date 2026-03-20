package tools

import (
	"context"
	"encoding/json"
)

type ToolContext struct {
	UserID string
}

// Tool defines the shared interface for all agent actions.
type Tool interface {
	Name() string
	Description() string
	Parameters() string // Returns JSON schema of required/optional arguments
	Execute(ctx context.Context, params json.RawMessage) (string, error)
	RequiresConfirmation() bool
}
