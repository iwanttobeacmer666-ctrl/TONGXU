package groupassistant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/chenhg5/cc-connect/core"
)

type modelPicker struct {
	Group   string
	Models  []core.NativeModel
	Efforts []string
	Model   string
	At      time.Time
}

func (s *Service) inspect(ctx context.Context, id Identity, kind string) (NativeInspection, error) {
	runner, ok := s.runner.(ControlRunner)
	if !ok {
		return NativeInspection{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	select {
	case s.inspectGate <- struct{}{}:
		defer func() { <-s.inspectGate }()
	case <-ctx.Done():
		return NativeInspection{}, ctx.Err()
	}
	if err := os.MkdirAll(s.cfg.WorkDir, 0700); err != nil {
		return NativeInspection{}, err
	}
	dir, err := os.MkdirTemp(s.cfg.WorkDir, "control-")
	if err != nil {
		return NativeInspection{}, err
	}
	defer os.RemoveAll(dir)
	if err := os.Mkdir(filepath.Join(dir, ".ccga-tmp"), 0700); err != nil {
		return NativeInspection{}, err
	}
	return runner.Inspect(ctx, &Snapshot{Dir: dir, Identity: id, Settings: s.chat.Settings(id.Group)}, kind)
}

func pickerKey(id Identity, kind string) string { return conversationKey(id) + ":" + kind }
func safeLabel(value string) string {
	return clip(strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, value), 200)
}
func (s *Service) nativeDefault(value string) string {
	if value != "" {
		return safeLabel(value)
	}
	return s.T(core.MsgGroupNativeDefault)
}

func (s *Service) controlFailure(err error) Response {
	if errors.Is(err, context.DeadlineExceeded) {
		return Response{Text: s.T(core.MsgGroupQueryTimeout)}
	}
	return s.failure(err)
}

func (s *Service) controlCommand(ctx context.Context, id Identity, command, arg string) Response {
	if command == "/thinkng" {
		command = "/thinking"
	}
	if command == "/token" {
		if arg != "" {
			return s.failure(ErrInvalid)
		}
		result, err := s.inspect(ctx, id, "quota")
		if err != nil {
			return s.controlFailure(err)
		}
		return Response{Text: s.quotaText(result.Quota)}
	}
	if arg != "" {
		if id.User != "wecom:"+s.cfg.Owner {
			return s.failure(ErrDenied)
		}
		s.selectionMu.Lock()
		defer s.selectionMu.Unlock()
		number, err := strconv.Atoi(arg)
		if err != nil || number < 1 {
			return s.failure(ErrInvalid)
		}
		s.mu.Lock()
		picker, exists := s.pickers[pickerKey(id, command)]
		s.mu.Unlock()
		if !exists || time.Since(picker.At) > 10*time.Minute {
			return Response{Text: s.T(core.MsgGroupPickFirst, command)}
		}
		settings := s.chat.Settings(id.Group)
		if command == "/model" {
			if number > len(picker.Models) {
				return s.failure(ErrInvalid)
			}
			model := picker.Models[number-1]
			settings = GroupSettings{Model: model.Model, Effort: model.DefaultReasoningEffort}
		} else {
			if number > len(picker.Efforts) || settings.Model != "" && settings.Model != picker.Model {
				return Response{Text: s.T(core.MsgGroupPickFirst, command)}
			}
			settings.Model = picker.Model
			settings.Effort = picker.Efforts[number-1]
		}
		if err := s.chat.SetSettings(id.Group, settings); err != nil {
			return s.failure(err)
		}
		// Invalidate every member's effort menu after a model/settings change.
		s.mu.Lock()
		for key := range s.pickers {
			if strings.HasSuffix(key, ":/thinking") && s.pickers[key].Group == id.Group {
				delete(s.pickers, key)
			}
		}
		s.mu.Unlock()
		return Response{Text: s.T(core.MsgGroupSettingSaved, s.nativeDefault(settings.Model), s.nativeDefault(settings.Effort))}
	}
	result, err := s.inspect(ctx, id, "models")
	if err != nil {
		return s.controlFailure(err)
	}
	if len(result.Models) == 0 {
		return Response{Text: s.T(core.MsgGroupNoNativeData)}
	}
	picker := modelPicker{Models: result.Models, At: time.Now()}
	if command == "/model" {
		var lines []string
		for i, model := range result.Models {
			label := model.DisplayName
			if label == "" {
				label = model.Model
			}
			lines = append(lines, fmt.Sprintf("%d. %s [%s]", i+1, safeLabel(label), safeLabel(model.Model)))
		}
		s.mu.Lock()
		picker.Group = id.Group
		s.pickers[pickerKey(id, command)] = picker
		s.mu.Unlock()
		return Response{Text: s.T(core.MsgGroupModelPicker, s.nativeDefault(result.Current.Model), s.nativeDefault(result.Current.Effort), strings.Join(lines, "\n"))}
	}
	for _, model := range result.Models {
		if model.Model != result.Current.Model {
			continue
		}
		picker.Model = model.Model
		for _, effort := range model.SupportedReasoningEfforts {
			value := effort.ReasoningEffort
			if value != "" && len(value) <= 64 && !strings.ContainsAny(value, "\x00\r\n") {
				picker.Efforts = append(picker.Efforts, value)
			}
		}
	}
	if len(picker.Efforts) == 0 {
		return Response{Text: s.T(core.MsgGroupNoNativeData)}
	}
	var lines []string
	for i, value := range picker.Efforts {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, safeLabel(value)))
	}
	s.mu.Lock()
	picker.Group = id.Group
	s.pickers[pickerKey(id, command)] = picker
	s.mu.Unlock()
	return Response{Text: s.T(core.MsgGroupThinkingPicker, s.nativeDefault(picker.Model), s.nativeDefault(result.Current.Effort), strings.Join(lines, "\n"))}
}

func (s *Service) displayTime(when time.Time) string {
	zone := s.cfg.Timezone
	if zone == "" {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
		zone = "UTC"
	}
	return when.In(location).Format("2006-01-02 15:04:05") + " " + zone
}

func (s *Service) contextText(id Identity) string {
	item := s.chat.Conversation(id)
	if item.ThreadID == "" {
		return s.T(core.MsgGroupNoConversation)
	}
	selected := s.chat.Settings(id.Group)
	if item.Context == nil || item.Context.ContextWindow <= 0 || item.Context.UsedTokens < 0 || item.ObservedAt.IsZero() || selected.Model != "" && selected.Model != item.Model {
		return s.T(core.MsgGroupNoNativeData)
	}
	usage := item.Context
	remaining := 100 * (1 - float64(usage.UsedTokens)/float64(usage.ContextWindow))
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}
	return s.T(core.MsgGroupContextInfo, safeLabel(item.Model), usage.UsedTokens, usage.ContextWindow, remaining, s.displayTime(item.ObservedAt))
}

func (s *Service) quotaText(quotas []core.NativeQuota) string {
	var lines []string
	for _, quota := range quotas {
		for i, window := range quota.Windows {
			remaining, reset := "?", "?"
			if window.UsedPercent != nil {
				used := *window.UsedPercent
				if used < 0 {
					used = 0
				}
				if used > 100 {
					used = 100
				}
				remaining = fmt.Sprintf("%d%%", 100-used)
			}
			if window.ResetsAt != nil && *window.ResetsAt > 0 {
				reset = s.displayTime(time.Unix(*window.ResetsAt, 0))
			}
			name := fmt.Sprintf("%s #%d", safeLabel(quota.Name), i+1)
			if window.WindowDurationMins != nil {
				name += fmt.Sprintf(" (%d min)", *window.WindowDurationMins)
			}
			lines = append(lines, s.T(core.MsgGroupQuotaLine, name, remaining, reset))
		}
	}
	if len(lines) == 0 {
		return s.T(core.MsgGroupNoNativeData)
	}
	return s.T(core.MsgGroupQuotaHeader) + "\n" + strings.Join(lines, "\n")
}
