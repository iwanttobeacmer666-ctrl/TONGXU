package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

type appServerTools struct {
	specs       []core.DynamicTool
	handler     core.DynamicToolHandler
	controlOnly bool
}

func parseDynamicTools(opts map[string]any, backend, profile string) (appServerTools, error) {
	specs, _ := opts["dynamic_tools"].([]core.DynamicTool)
	handler, _ := opts["dynamic_tool_handler"].(core.DynamicToolHandler)
	controlOnly, _ := opts["native_control_only"].(bool)
	if controlOnly {
		if backend != "app_server" || profile == "" || len(specs) > 0 || handler != nil {
			return appServerTools{}, fmt.Errorf("native inspection requires an isolated app_server without execution tools")
		}
		return appServerTools{controlOnly: true}, nil
	}
	if len(specs) == 0 && handler == nil {
		return appServerTools{}, nil
	}
	if backend != "app_server" || profile == "" || len(specs) == 0 || len(specs) > 16 || handler == nil {
		return appServerTools{}, fmt.Errorf("codex: dynamic tools require a named app_server boundary and trusted handler")
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.Type != "function" || spec.Name == "" || strings.ContainsAny(spec.Name, " /\\\x00") || seen[spec.Name] || spec.InputSchema == nil {
			return appServerTools{}, fmt.Errorf("codex: invalid dynamic tool specification")
		}
		seen[spec.Name] = true
	}
	return appServerTools{specs: append([]core.DynamicTool(nil), specs...), handler: handler}, nil
}

func (s *appServerSession) handleDynamicToolCall(rawID json.RawMessage, paramsRaw json.RawMessage) {
	var params struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace *string         `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	reply := func(success bool, text string) {
		_ = s.writeJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": map[string]any{
			"success": success, "contentItems": []map[string]any{{"type": "inputText", "text": text}},
		}})
	}
	if len(paramsRaw) > 64<<10 || json.Unmarshal(paramsRaw, &params) != nil || s.tools.handler == nil || params.CallID == "" || params.Namespace != nil {
		reply(false, "tool not available on this client")
		return
	}
	allowed := false
	for _, spec := range s.tools.specs {
		allowed = allowed || spec.Name == params.Tool
	}
	s.stateMu.Lock()
	if !allowed || params.ThreadID != s.CurrentSessionID() || params.TurnID == "" || params.TurnID != s.currentTurn || len(s.dynamicCalls) >= 64 || s.dynamicCalls[params.CallID] {
		s.stateMu.Unlock()
		reply(false, "tool request is outside the active session")
		return
	}
	if s.dynamicCalls == nil {
		s.dynamicCalls = map[string]bool{}
	}
	s.dynamicCalls[params.CallID] = true
	s.stateMu.Unlock()
	// Never block the protocol reader: owner decisions arrive while this runs.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		text, err := s.tools.handler(s.ctx, params.Tool, params.Arguments)
		if err != nil {
			// The callback returns only safe public errors, never raw HTTP or
			// package-manager output containing host details or credentials.
			reply(false, err.Error())
			return
		}
		if len(text) > 128<<10 {
			text = truncate(text, 128<<10)
		}
		reply(true, text)
	}()
}
