package groupassistant

import (
	"context"
	"errors"

	"github.com/chenhg5/cc-connect/core"
)

type NativeInspection struct {
	Models  []core.NativeModel
	Quota   []core.NativeQuota
	Current GroupSettings
}
type ControlRunner interface {
	Inspect(context.Context, *Snapshot, string) (NativeInspection, error)
}

func (n *NativeRunner) Inspect(ctx context.Context, snap *Snapshot, kind string) (result NativeInspection, err error) {
	if err = n.Ready(); err != nil {
		return result, err
	}
	// A stale or unavailable group selection must not prevent listing models
	// or quota for recovery. This inspection thread uses dedicated defaults.
	query := *snap
	query.Settings = GroupSettings{}
	opts, err := n.agentOptions(&query)
	if err != nil {
		return result, err
	}
	delete(opts, "dynamic_tools")
	delete(opts, "dynamic_tool_handler")
	opts["native_control_only"] = true
	agent, err := core.CreateAgent("codex", opts)
	if err != nil {
		return result, err
	}
	defer agent.Stop()
	session, err := startNativeSession(ctx, agent, "")
	if err != nil {
		var failure *core.AgentFailure
		if !errors.As(err, &failure) || failure.Kind == core.AgentBoundary {
			n.unhealthy.Store(true)
		}
		return result, err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			n.unhealthy.Store(true)
			err = errors.Join(err, &core.AgentFailure{Kind: core.AgentBoundary, Cause: closeErr})
		}
	}()
	control, ok := session.(core.NativeSessionControl)
	if !ok {
		return result, ErrUnavailable
	}
	if runtimeSession, ok := session.(interface {
		GetModel() string
		GetReasoningEffort() string
	}); ok {
		result.Current = GroupSettings{runtimeSession.GetModel(), runtimeSession.GetReasoningEffort()}
	}
	if snap.Settings.Model != "" {
		result.Current = snap.Settings
	}
	switch kind {
	case "handshake":
		// Session startup verifies the profile/reviewer, cwd and runtime roots.
		// No model inference, quota query or tool call is needed.
	case "models":
		result.Models, err = control.ListNativeModels(ctx)
	case "quota":
		result.Quota, err = control.ReadNativeQuota(ctx)
	default:
		err = ErrInvalid
	}
	return result, err
}
