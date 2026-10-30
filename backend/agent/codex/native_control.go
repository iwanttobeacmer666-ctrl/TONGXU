package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func controlTimeout(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	timeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return 0, context.DeadlineExceeded
	}
	return timeout, nil
}

func (s *appServerSession) ListNativeModels(ctx context.Context) ([]core.NativeModel, error) {
	var models []core.NativeModel
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 10; page++ {
		timeout, err := controlTimeout(ctx)
		if err != nil {
			return nil, err
		}
		var response struct {
			Data       []core.NativeModel `json:"data"`
			NextCursor *string            `json:"nextCursor"`
		}
		params := map[string]any{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := s.requestWithTimeout("model/list", params, &response, timeout); err != nil {
			return nil, err
		}
		for _, model := range response.Data {
			if model.Hidden || model.Model == "" || seen[model.Model] {
				continue
			}
			if len(model.Model) > 200 || strings.ContainsAny(model.Model, "\x00\r\n") {
				return nil, fmt.Errorf("invalid model catalog")
			}
			seen[model.Model] = true
			models = append(models, model)
			if len(models) > 500 {
				return nil, fmt.Errorf("model catalog too large")
			}
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			return models, nil
		}
		if *response.NextCursor == cursor {
			return nil, fmt.Errorf("model catalog cursor repeated")
		}
		cursor = *response.NextCursor
	}
	return nil, fmt.Errorf("model catalog pagination limit")
}

func (s *appServerSession) ReadNativeQuota(ctx context.Context) ([]core.NativeQuota, error) {
	timeout, err := controlTimeout(ctx)
	if err != nil {
		return nil, err
	}
	type snapshot struct {
		LimitID   string                  `json:"limitId"`
		LimitName string                  `json:"limitName"`
		Primary   *core.NativeQuotaWindow `json:"primary"`
		Secondary *core.NativeQuotaWindow `json:"secondary"`
	}
	var response struct {
		RateLimits *snapshot           `json:"rateLimits"`
		ByID       map[string]snapshot `json:"rateLimitsByLimitId"`
	}
	if err := s.requestWithTimeout("account/rateLimits/read", map[string]any{}, &response, timeout); err != nil {
		return nil, err
	}
	if len(response.ByID) == 0 && response.RateLimits != nil {
		response.ByID = map[string]snapshot{"codex": *response.RateLimits}
	}
	var keys []string
	for key := range response.ByID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []core.NativeQuota
	for _, key := range keys {
		item := response.ByID[key]
		name := item.LimitName
		if name == "" {
			name = item.LimitID
		}
		if name == "" {
			name = key
		}
		quota := core.NativeQuota{Name: name}
		for _, window := range []*core.NativeQuotaWindow{item.Primary, item.Secondary} {
			if window != nil {
				quota.Windows = append(quota.Windows, *window)
			}
		}
		if len(quota.Windows) > 0 {
			result = append(result, quota)
		}
	}
	return result, nil
}

func (s *appServerSession) StartCompaction() error {
	if s.CurrentSessionID() == "" {
		return fmt.Errorf("no conversation to compact")
	}
	s.stateMu.Lock()
	busy := s.currentTurn != ""
	s.stateMu.Unlock()
	if busy {
		return &core.AgentFailure{Kind: core.AgentBusy}
	}
	return s.request("thread/compact/start", map[string]any{"threadId": s.CurrentSessionID()}, nil)
}

func (s *appServerSession) verifyThreadWorkspace(cwd string, roots []string) error {
	if s.permissionProfile == "" {
		return nil
	}
	want, err := filepath.EvalSymlinks(s.GetWorkDir())
	if err != nil {
		return &core.AgentFailure{Kind: core.AgentBoundary, Cause: err}
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil || !strings.EqualFold(filepath.Clean(actual), filepath.Clean(want)) {
		return &core.AgentFailure{Kind: core.AgentBoundary, Cause: fmt.Errorf("native workspace mismatch")}
	}
	if len(roots) != 1 || !strings.EqualFold(filepath.Clean(roots[0]), filepath.Clean(want)) {
		return &core.AgentFailure{Kind: core.AgentBoundary, Cause: fmt.Errorf("native runtime roots mismatch")}
	}
	return nil
}

func (s *appServerSession) verifyThreadChoice(model string, effort *string) error {
	if s.permissionProfile == "" {
		return nil
	}
	if requested := s.GetModel(); requested != "" && requested != model {
		return &core.AgentFailure{Kind: core.AgentFailed, Cause: fmt.Errorf("native did not confirm selected model")}
	}
	if requested := s.GetReasoningEffort(); requested != "" && (effort == nil || requested != *effort) {
		return &core.AgentFailure{Kind: core.AgentFailed, Cause: fmt.Errorf("native did not confirm selected effort")}
	}
	return nil
}

// Use protocol error codes, never arbitrary stderr keyword matching.
func nativeProtocolFailure(raw json.RawMessage, cause error) error {
	kind := core.AgentFailed
	var name string
	var nested struct {
		Info json.RawMessage `json:"codexErrorInfo"`
	}
	if json.Unmarshal(raw, &nested) == nil && len(nested.Info) > 0 {
		raw = nested.Info
	}
	_ = json.Unmarshal(raw, &name)
	if name == "" {
		var variants map[string]json.RawMessage
		_ = json.Unmarshal(raw, &variants)
		for key, value := range variants {
			switch key {
			case "httpConnectionFailed", "responseStreamConnectionFailed", "responseStreamDisconnected", "responseTooManyFailedAttempts":
				kind = core.AgentConnection
				var status struct {
					HTTPStatusCode *int `json:"httpStatusCode"`
				}
				_ = json.Unmarshal(value, &status)
				if status.HTTPStatusCode != nil {
					switch *status.HTTPStatusCode {
					case 401, 403:
						kind = core.AgentLogin
					case 429:
						kind = core.AgentBusy
					case 503:
						kind = core.AgentBusy
					}
				}
			}
		}
	}
	switch name {
	case "usageLimitExceeded", "sessionBudgetExceeded":
		kind = core.AgentQuota
	case "rateLimitExceeded", "serverOverloaded", "flexUnavailable":
		kind = core.AgentBusy
	case "unauthorized":
		kind = core.AgentLogin
	case "sandboxError":
		kind = core.AgentBoundary
	}
	return &core.AgentFailure{Kind: kind, Cause: cause}
}

func (s *appServerSession) beforeSubmissionFailure(err error) error {
	if cleanup := s.Close(); cleanup != nil {
		return &core.AgentFailure{Kind: core.AgentBoundary, Cause: errors.Join(err, cleanup)}
	}
	var known *core.AgentFailure
	if errors.As(err, &known) && known.Kind == core.AgentConnection {
		return &core.AgentFailure{Kind: core.AgentConnection, BeforeSubmission: true, Cause: err}
	}
	return err
}

func connectionFailure(err error) error {
	if err == nil {
		return nil
	}
	var known *core.AgentFailure
	if errors.As(err, &known) {
		return err
	}
	return &core.AgentFailure{Kind: core.AgentConnection, Cause: err}
}
