package groupassistant

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type TaskEvent struct {
	Sequence uint64
	At       time.Time
	Kind     string
	Text     string
}
type TaskView struct {
	ID                      string
	Actor                   Identity
	State                   string
	Created, Started, Ended time.Time
	CanStop                 bool
	Events                  []TaskEvent `json:",omitempty"`
}

var credentialAssignment = regexp.MustCompile(`(?i)((?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|password|secret|authorization)\s*[=:]\s*)[^\r\n,;]+`)
var bearerCredential = regexp.MustCompile(`(?i)(?:bearer|basic)\s+[^\s"']+|sk-[a-zA-Z0-9_-]{16,}`)

func redactProgress(text string) string {
	for _, key := range []string{"CC_GROUP_BOT_SECRET", "OPENAI_API_KEY", "AZURE_OPENAI_API_KEY"} {
		if secret := os.Getenv(key); secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	text = credentialAssignment.ReplaceAllString(text, "${1}[redacted]")
	text = bearerCredential.ReplaceAllString(text, "[redacted]")
	// 40 events remain below the pipe frame bound even when JSON expands
	// every byte into a six-byte escape sequence.
	return clip(text, 1000)
}

func (task *groupTask) record(kind, text string) {
	task.mu.Lock()
	defer task.mu.Unlock()
	task.sequence++
	task.events = append(task.events, TaskEvent{task.sequence, time.Now().UTC(), kind, redactProgress(text)})
	if len(task.events) > 40 {
		task.events = task.events[len(task.events)-40:]
	}
}

func (task *groupTask) waitForApproval(wait func() (bool, error)) (bool, error) {
	task.mu.Lock()
	if !task.stopping {
		task.state = "waiting_approval"
	}
	task.mu.Unlock()
	task.record("waiting_approval", "")
	defer func() {
		task.mu.Lock()
		if task.state == "waiting_approval" {
			task.state = "running"
		}
		task.mu.Unlock()
	}()
	return wait()
}

// Only public execution events enter the management projection. Reasoning and
// arbitrary raw event maps never enter the task viewer.
func (task *groupTask) observe(event core.Event) {
	switch event.Type {
	case core.EventToolUse:
		task.record("tool_started", event.ToolName+"\n"+event.ToolInput)
	case core.EventToolResult:
		task.record("tool_finished", event.ToolName+"\n"+event.ToolResult)
	case core.EventText:
		task.record("output", event.Content)
	case core.EventPermissionRequest:
		task.record("permission", event.ToolName)
	case core.EventError:
		task.record("error", "execution failed")
	}
}

func (task *groupTask) view(detail bool) TaskView {
	task.mu.Lock()
	defer task.mu.Unlock()
	v := TaskView{ID: task.code, Actor: task.actor, State: task.state, Created: task.created, Started: task.started, Ended: task.ended,
		CanStop: task.ended.IsZero() && !task.stopping && task.state != "publishing"}
	if detail {
		v.Events = append([]TaskEvent(nil), task.events...)
	}
	return v
}

func (s *Service) AdminTasks(owner, group, code string) ([]TaskView, error) {
	if owner != s.store.admin {
		return nil, ErrDenied
	}
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	var tasks []*groupTask
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}
	tasks = append(tasks, s.recentTasks...)
	var result []TaskView
	for _, task := range tasks {
		if (group == "" || task.actor.Group == group) && (code == "" || task.code == code) {
			result = append(result, task.view(code != ""))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created.After(result[j].Created) })
	if code != "" && len(result) == 0 {
		return nil, ErrInvalid
	}
	return result, nil
}

func (s *Service) AdminStop(ctx context.Context, owner, code string) Response {
	if owner != s.store.admin {
		return s.failure(ErrDenied)
	}
	s.tasksMu.Lock()
	task := s.tasks[code]
	s.tasksMu.Unlock()
	if task == nil {
		return s.failure(ErrInvalid)
	}
	return s.taskCommand(ctx, Identity{Group: task.actor.Group, User: owner}, "/stop", code)
}
