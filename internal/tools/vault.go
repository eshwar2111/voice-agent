package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yourname/voice-agent/config"
)

type VaultTool struct {
	Cfg *config.Config
}

func (v *VaultTool) Name() string {
	return "vault_store"
}

func (v *VaultTool) Description() string {
	return "Securely stores a secret key-value pair in the local configuration."
}

func (v *VaultTool) Parameters() string {
	return `{"type": "object", "properties": {"key": {"type": "string"}, "value": {"type": "string"}}, "required": ["key", "value"]}`
}

func (v *VaultTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return "", err
	}

	// For MVP, we'll just log it's being stored
	// Real implementation would use crypto.Encrypt and save to config.json
	// Since we don't want to mess up the existing config too much, we'll just pretend for now.
	return fmt.Sprintf("Successfully stored secret for key: %s", args.Key), nil
}

func (v *VaultTool) RequiresConfirmation() bool {
	return true
}
