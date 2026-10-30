package core

import "context"

const EventNativeContextUsage EventType = "native_context_usage"

// NativeModel is a server-returned picker entry, not an entitlement guarantee.
type NativeModel struct {
	ID                        string                  `json:"id"`
	Model                     string                  `json:"model"`
	DisplayName               string                  `json:"displayName"`
	Description               string                  `json:"description"`
	Hidden                    bool                    `json:"hidden"`
	IsDefault                 bool                    `json:"isDefault"`
	DefaultReasoningEffort    string                  `json:"defaultReasoningEffort"`
	SupportedReasoningEfforts []NativeReasoningEffort `json:"supportedReasoningEfforts"`
}
type NativeReasoningEffort struct {
	ReasoningEffort string `json:"reasoningEffort"`
	Description     string `json:"description"`
}

// NativeSessionControl uses the same dedicated server and credentials as work.
type NativeSessionControl interface {
	ListNativeModels(context.Context) ([]NativeModel, error)
	ReadNativeQuota(context.Context) ([]NativeQuota, error)
	StartCompaction() error
}
type NativeQuota struct {
	Name    string
	Windows []NativeQuotaWindow
}
type NativeQuotaWindow struct {
	UsedPercent        *int   `json:"usedPercent"`
	WindowDurationMins *int   `json:"windowDurationMins"`
	ResetsAt           *int64 `json:"resetsAt"`
}

type AgentFailureKind string

const (
	AgentConnection AgentFailureKind = "connection"
	AgentBusy       AgentFailureKind = "busy"
	AgentLogin      AgentFailureKind = "login"
	AgentQuota      AgentFailureKind = "quota"
	AgentBoundary   AgentFailureKind = "boundary"
	AgentFailed     AgentFailureKind = "failed"
)

// BeforeSubmission is set only by the trusted runner, after successful cleanup.
// A lost turn/start reply can never establish that execution did not begin.
type AgentFailure struct {
	Kind             AgentFailureKind
	BeforeSubmission bool
	Cause            error
}

func (e *AgentFailure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "native agent: " + string(e.Kind)
}
func (e *AgentFailure) Unwrap() error { return e.Cause }
