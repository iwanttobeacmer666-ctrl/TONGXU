package core

import (
	"context"
	"encoding/json"
)

// DynamicTool describes an additional client-implemented tool. It supplements
// native tools and never changes their resource permissions.
type DynamicTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// DynamicToolHandler is bound by the trusted caller to one actor and session.
// User/model arguments must not supply or replace that identity.
type DynamicToolHandler func(context.Context, string, json.RawMessage) (string, error)

// BoundedCommandRunner runs an existing tool under the agent's explicitly
// configured native permissions, without a model or an unrestricted fallback.
type BoundedCommandRunner interface {
	RunBoundedCommand(context.Context, string, []string, map[string]string) error
}
