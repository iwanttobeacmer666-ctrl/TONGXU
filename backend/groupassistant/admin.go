package groupassistant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type groupTask struct {
	mu               sync.Mutex
	code             string
	actor            Identity
	state            string
	created, started time.Time
	stopping         bool
	cancel           context.CancelFunc
	done             chan struct{}
	cleanupErr       error // written by task goroutine, read after done closes
	completed        bool
	events           []TaskEvent
	sequence         uint64
	ended            time.Time
}

var errMemberBusy = errors.New("member already has an unfinished task")

// LocalTaskCommand is called only by the authenticated local management broker.
func (s *Service) LocalTaskCommand(ctx context.Context, id Identity, action, task string) Response {
	if action == "tasks" {
		return s.taskCommand(ctx, id, "/tasks", "")
	}
	return s.taskCommand(ctx, id, "/stop", task)
}

func (s *Service) addTask(ctx context.Context, actor Identity) (*groupTask, context.Context, error) {
	code, err := token()
	if err != nil {
		return nil, nil, err
	}
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for _, previous := range s.revocations[actor] {
		select {
		case <-previous.done:
			if previous.cleanupErr != nil {
				return nil, nil, ErrUnavailable
			}
		default:
			return nil, nil, ErrUnavailable
		}
	}
	// The account and global queue are shared across groups. A stable ordinary
	// user cannot bypass the personal limit by submitting in another group.
	if actor.User != s.store.admin {
		for _, existing := range s.tasks {
			if existing.actor.User == actor.User {
				return nil, nil, errMemberBusy
			}
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	task := &groupTask{code: code, actor: actor, state: "queued", created: time.Now(), cancel: cancel, done: make(chan struct{})}
	if s.tasks == nil {
		s.tasks = map[string]*groupTask{}
	}
	s.tasks[code] = task
	task.record("queued", "")
	return task, ctx, nil
}

func (s *Service) finishTask(task *groupTask) {
	task.cancel()
	s.tasksMu.Lock()
	task.mu.Lock()
	task.ended = time.Now().UTC()
	switch {
	case task.cleanupErr != nil:
		task.state = "cleanup_failed"
	case task.stopping:
		task.state = "stopped"
	case task.completed:
		task.state = "completed"
	default:
		task.state = "failed"
	}
	terminal := task.state
	task.mu.Unlock()
	task.record(terminal, "")
	if task.cleanupErr != nil {
		if s.revocations == nil {
			s.revocations = map[Identity][]*groupTask{}
		}
		s.revocations[task.actor] = append(s.revocations[task.actor], task)
	}
	delete(s.tasks, task.code)
	s.recentTasks = append(s.recentTasks, task)
	if len(s.recentTasks) > 50 {
		s.recentTasks = s.recentTasks[len(s.recentTasks)-50:]
	}
	close(task.done)
	s.tasksMu.Unlock()
}

func cleanupUncertain(err error) bool {
	if err == nil || onlyCancellation(err) {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range many.Unwrap() {
			if cleanupUncertain(child) {
				return true
			}
		}
		return false
	}
	if failure, ok := err.(*core.AgentFailure); ok {
		return failure.Kind == core.AgentBoundary
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return cleanupUncertain(one.Unwrap())
	}
	return true
}

// Joined cleanup errors must never be hidden by a cancellation error.
func onlyCancellation(err error) bool {
	if err == nil {
		return true
	}
	if failure, ok := err.(*core.AgentFailure); ok && failure.Kind == core.AgentBoundary {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range many.Unwrap() {
			if !onlyCancellation(child) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return onlyCancellation(one.Unwrap())
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (s *Service) taskCommand(ctx context.Context, id Identity, command, arg string) Response {
	if id.User != s.store.admin {
		return s.failure(ErrDenied)
	}
	s.tasksMu.Lock()
	if command == "/tasks" {
		if arg != "" {
			s.tasksMu.Unlock()
			return s.failure(ErrInvalid)
		}
		var lines []string
		for _, task := range s.tasks {
			if task.actor.Group != id.Group {
				continue
			}
			task.mu.Lock()
			lines = append(lines, fmt.Sprintf("%s | %s | %s | %s | %s", task.code, strings.TrimPrefix(task.actor.User, "wecom:"), task.state, s.displayTime(task.created), func() string {
				if task.started.IsZero() {
					return "-"
				}
				return s.displayTime(task.started)
			}()))
			task.mu.Unlock()
		}
		s.tasksMu.Unlock()
		sort.Strings(lines)
		return Response{Text: s.T(core.MsgGroupTasks) + "\n" + strings.Join(lines, "\n"), Followup: true, ControlOK: true}
	}
	task := s.tasks[arg]
	s.tasksMu.Unlock()
	if task == nil || task.actor.Group != id.Group {
		return s.failure(ErrDenied)
	}
	task.mu.Lock()
	if !task.ended.IsZero() {
		task.mu.Unlock()
		return s.failure(ErrInvalid)
	}
	if task.state == "publishing" {
		task.mu.Unlock()
		return Response{Text: s.T(core.MsgGroupTaskPublishing)}
	}
	task.stopping = true
	task.state = "stopping"
	task.cancel()
	task.mu.Unlock()
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	select {
	case <-task.done:
		if task.cleanupErr != nil {
			return Response{Text: s.T(core.MsgGroupTaskUncertain), Followup: true}
		}
		return Response{Text: s.T(core.MsgGroupTaskStopped), Followup: true, ControlOK: true}
	case <-ctx.Done():
		return Response{Text: s.T(core.MsgGroupTaskUncertain), Followup: true}
	case <-timer.C:
		return Response{Text: s.T(core.MsgGroupTaskUncertain), Followup: true}
	}
}

func (s *Service) adminSnapshot(id Identity, text string) (*Snapshot, error) {
	s.store.mu.Lock()
	known := append([]string(nil), s.store.members[id.Group]...)
	s.store.mu.Unlock()
	for _, user := range s.memberIDs(id.Group) {
		known = append(known, "wecom:"+user)
	}
	selected := []string{id.User, "public"}
	seen := map[string]bool{id.User: true, "public": true}
	all := strings.Contains(text, "所有成员") || strings.Contains(text, "全部成员")
	for _, owner := range known {
		user := strings.TrimPrefix(owner, "wecom:")
		named := regexp.MustCompile(`(^|[^\pL\pN_\-])` + regexp.QuoteMeta(user) + `($|[^\pL\pN_\-])`).MatchString(text)
		if !seen[owner] && (named || all) {
			selected = append(selected, owner)
			seen[owner] = true
		}
	}
	return s.store.snapshot(id, s.cfg.WorkDir, selected, &text)
}
