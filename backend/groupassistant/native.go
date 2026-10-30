package groupassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type Runner interface {
	Run(context.Context, *Snapshot, string) (string, error)
}
type NativeRunner struct {
	Command, Home, WindowsSandbox, Model, Effort string
	ToolRoots, Hosts                             []string
	Verified                                     bool
	AutomaticRoutine                             bool
	Admin                                        string
	unhealthy                                    atomic.Bool
}

func (n *NativeRunner) Ready() error {
	if !n.Verified || n.unhealthy.Load() || n.Command == "" || n.Home == "" {
		return ErrUnavailable
	}
	if runtime.GOOS != "windows" {
		return ErrUnavailable
	}
	if !filepath.IsAbs(n.Command) || !filepath.IsAbs(n.Home) {
		return ErrInvalid
	}
	// Never enter legacy account/ACL/firewall provisioning for group work.
	if n.WindowsSandbox != "mxc" {
		return ErrUnavailable
	}
	return nil
}
func (n *NativeRunner) agentOptions(snap *Snapshot) (map[string]any, error) {
	hosts := n.Hosts
	if n.AutomaticRoutine {
		hosts = nil
	}
	roots := append(append([]string(nil), n.ToolRoots...), snap.ReadOnlyRoots...)
	args, e := BoundaryArgs(snap.Dir, filepath.Join(snap.Dir, "public"), n.Home, roots, hosts, snap.Identity.User == n.Admin)
	if e != nil {
		return nil, e
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-c", "windows.sandbox=\""+n.WindowsSandbox+"\"")
		if len(hosts) > 0 {
			// Explicitly selected MXC requires loopback for its managed proxy.
			args = append(args, "-c", "permissions.cc_member.network.allow_local_binding=true")
		}
	}
	args = append(args, "-c", "cli_auth_credentials_store=\"file\"")
	command := append([]string{n.Command}, args...)
	model := snap.Settings.Model
	if model == "" {
		model = n.Model
	}
	effort := snap.Settings.Effort
	if snap.Settings.Model == "" && effort == "" {
		effort = n.Effort
	}
	return map[string]any{
		"cmd": command, "backend": "app_server", "permission_profile": "cc_member",
		"approval_policy": "untrusted",
		"mode":            "full-auto", "work_dir": snap.Dir, "codex_home": n.Home, "model": model, "reasoning_effort": effort,
		"env":           map[string]string{"TEMP": filepath.Join(snap.Dir, ".ccga-tmp"), "TMP": filepath.Join(snap.Dir, ".ccga-tmp"), "TMPDIR": filepath.Join(snap.Dir, ".ccga-tmp")},
		"dynamic_tools": snap.DynamicTools, "dynamic_tool_handler": snap.DynamicHandler,
	}, nil
}

// Only handshake transport loss is retried, after cleanup confirms no process
// remains. No user turn or compaction has been submitted at this point.
func startNativeSession(ctx context.Context, agent core.Agent, resume string) (core.AgentSession, error) {
	for attempt := 0; attempt < 2; attempt++ {
		session, err := agent.StartSession(ctx, resume)
		if err == nil {
			return session, nil
		}
		var failure *core.AgentFailure
		if attempt != 0 || !errors.As(err, &failure) || !failure.BeforeSubmission || failure.Kind != core.AgentConnection || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, ErrUnavailable
}

func (n *NativeRunner) Run(ctx context.Context, snap *Snapshot, prompt string) (reply string, err error) {
	if e := n.Ready(); e != nil {
		return "", e
	}
	if snap.Approve == nil {
		return "", ErrUnavailable
	}
	opts, e := n.agentOptions(snap)
	if e != nil {
		return "", e
	}
	agent, e := core.CreateAgent("codex", opts)
	if e != nil {
		n.unhealthy.Store(true)
		return "", e
	}
	defer agent.Stop()
	session, e := startNativeSession(ctx, agent, snap.ResumeID)
	if e != nil {
		// Startup may have launched tools before a protocol or cleanup error.
		// Require operator inspection before another task uses this runner.
		var failure *core.AgentFailure
		if !errors.As(e, &failure) || failure.Kind == core.AgentBoundary {
			n.unhealthy.Store(true)
		}
		return "", e
	}
	closed := false
	closeSession := func() error {
		if closed {
			return nil
		}
		closed = true
		if e := session.Close(); e != nil {
			n.unhealthy.Store(true)
			return &core.AgentFailure{Kind: core.AgentBoundary, Cause: e}
		}
		return nil
	}
	defer func() { err = errors.Join(err, closeSession()) }()
	report := func() error {
		if snap.Report == nil {
			return nil
		}
		model, effort := snap.Settings.Model, snap.Settings.Effort
		if runtimeSession, ok := session.(interface {
			GetModel() string
			GetReasoningEffort() string
		}); ok {
			model = runtimeSession.GetModel()
			effort = runtimeSession.GetReasoningEffort()
		}
		value := Conversation{Identity: snap.Identity, ThreadID: session.CurrentSessionID(), Model: model, Effort: effort}
		if reporter, ok := session.(core.ContextUsageReporter); ok {
			value.Context = reporter.GetContextUsage()
			if value.Context != nil {
				value.Context.BaselineTokens = 0
				value.ObservedAt = time.Now().UTC()
			}
		}
		return snap.Report(value)
	}
	if e = report(); e != nil {
		return "", e
	}
	if snap.Compact {
		control, ok := session.(core.NativeSessionControl)
		if !ok {
			return "", ErrUnavailable
		}
		e = control.StartCompaction()
	} else {
		e = session.Send(prompt, "", snap.Images, nil)
	}
	if e != nil {
		return "", e
	}
	compacted := false
	var text strings.Builder
	patches := map[string]any{}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-session.Events():
			if !ok {
				return "", fmt.Errorf("native agent ended before completing the turn")
			}
			if snap.Progress != nil {
				snap.Progress(event)
			}
			switch event.Type {
			case core.EventNativeContextUsage:
				if e = report(); e != nil {
					return "", e
				}
			case core.EventToolUse:
				if event.ToolName == "Patch" && len(patches) < 32 {
					item, _ := event.ToolInputRaw["itemId"].(string)
					data, marshalErr := json.Marshal(event.ToolInputRaw["changes"])
					if item != "" && marshalErr == nil && len(data) <= 64<<10 {
						patches[item] = event.ToolInputRaw["changes"]
					}
				}
			case core.EventToolResult:
				if event.ToolName == "ContextCompaction" {
					compacted = true
				}
			case core.EventText:
				if text.Len() < 128<<10 {
					text.WriteString(event.Content)
				}
			case core.EventPermissionRequest:
				if snap.Compact {
					_ = session.RespondPermission(event.RequestID, core.PermissionResult{Behavior: "deny"})
					return "", ErrDenied
				}
				if len(event.Questions) > 0 {
					return questionText(event), nil
				}
				decision := "deny"
				if event.ToolName == "Patch" {
					item, _ := event.ToolInputRaw["itemId"].(string)
					if changes, ok := patches[item]; ok {
						copy := make(map[string]any, len(event.ToolInputRaw)+1)
						for key, value := range event.ToolInputRaw {
							copy[key] = value
						}
						copy["changes"] = changes
						event.ToolInputRaw = copy
						delete(patches, item)
					}
				}
				_, _, reviewErr := ReviewableNativeAction(event, snap.Dir)
				if reviewErr != nil {
					slog.Debug("native approval is outside reviewable boundary",
						"tool", event.ToolName, "environment", event.ToolInputRaw["environmentId"],
						"kind", event.ToolInputRaw["kind"], "additional_permissions", event.ToolInputRaw["additionalPermissions"] != nil,
						"network_approval", event.ToolInputRaw["networkApprovalContext"] != nil,
						"has_command", event.ToolInputRaw["command"] != nil, "has_cwd", event.ToolInputRaw["cwd"] != nil)
				}
				if reviewErr == nil && snap.Approve != nil {
					approved, reviewErr := snap.Approve(ctx, event)
					if reviewErr != nil {
						_ = session.RespondPermission(event.RequestID, core.PermissionResult{Behavior: "deny"})
						return "", reviewErr
					}
					if approved {
						decision = "allow"
					}
				}
				if e = session.RespondPermission(event.RequestID, core.PermissionResult{Behavior: decision}); e != nil {
					return "", e
				}
			case core.EventError:
				if event.Error != nil {
					return "", event.Error
				}
				return "", fmt.Errorf("native agent turn failed")
			case core.EventResult:
				if snap.Compact && !compacted {
					return "", &core.AgentFailure{Kind: core.AgentFailed}
				}
				if event.Content != "" {
					reply = event.Content
				} else {
					reply = text.String()
				}
				if len(reply) > 128<<10 {
					reply = clip(reply, 128<<10)
				}
				if e = closeSession(); e != nil {
					return "", e
				}
				if e = report(); e != nil {
					return "", e
				}
				return reply, nil
			case core.EventUserInputRequest:
				return questionText(event), nil
			}
		}
	}
}
func questionText(event core.Event) string {
	var parts []string
	for _, q := range event.Questions {
		parts = append(parts, q.Question)
		for _, o := range q.Options {
			parts = append(parts, "- "+o.Label+": "+o.Description)
		}
	}
	if len(parts) == 0 {
		return event.Content
	}
	return strings.Join(parts, "\n")
}
