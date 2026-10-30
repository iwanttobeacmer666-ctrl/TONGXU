package groupassistant

import (
	"context"
	"os"
	"strings"
	"time"
)

func (s *Service) memberIDs(group string) []string {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return append([]string(nil), s.cfg.Members[group]...)
}

func (s *Service) actorEnabled(id Identity) bool {
	s.membersMu.RLock()
	defer s.membersMu.RUnlock()
	return rosterAllows(s.cfg.Groups, s.cfg.Members, s.store.admin, id)
}

func rosterAllows(groups []string, members map[string][]string, owner string, id Identity) bool {
	for _, group := range groups {
		if group != id.Group {
			continue
		}
		if id.User == owner {
			return true
		}
		for _, user := range members[group] {
			if id.User == "wecom:"+user {
				return true
			}
		}
	}
	return false
}

// ApplyMember executes persistence under the admission/publication fence. A
// failed save changes no permissions. Only the trusted local control plane calls it.
func (s *Service) ApplyMember(ctx context.Context, group, user string, enabled bool, persist func() error) error {
	if user == s.cfg.Owner || ValidateMembers(s.cfg.Owner, []string{group}, map[string][]string{group: {user}}) != nil {
		return ErrInvalid
	}
	s.accessMu.Lock()
	validGroup := false
	for _, g := range s.cfg.Groups {
		validGroup = validGroup || g == group
	}
	if !validGroup {
		s.accessMu.Unlock()
		return ErrDenied
	}
	members := map[string][]string{}
	for g, users := range s.cfg.Members {
		members[g] = append([]string(nil), users...)
	}
	var users []string
	for _, u := range members[group] {
		if u != user {
			users = append(users, u)
		}
	}
	if enabled {
		users = append(users, user)
	}
	members[group] = users
	stopped, err := s.applyRosterLocked(ctx, s.cfg.Groups, members, persist)
	s.accessMu.Unlock()
	if err != nil {
		return err
	}
	return waitRevokedTasks(ctx, stopped)
}

// ApplyRoster publishes one durable group/member change under the same fence
// used by admission and file publication. Empty active rosters are safe/idle.
func (s *Service) ApplyRoster(ctx context.Context, groups []string, members map[string][]string, persist func() error) error {
	s.accessMu.Lock()
	stopped, err := s.applyRosterLocked(ctx, groups, members, persist)
	s.accessMu.Unlock()
	if err != nil {
		return err
	}
	return waitRevokedTasks(ctx, stopped)
}

func (s *Service) applyRosterLocked(ctx context.Context, groups []string, members map[string][]string, persist func() error) ([]*groupTask, error) {
	if len(groups) > 16 || ValidateMembers(s.cfg.Owner, groups, members) != nil || persist == nil {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if !validIdentity(Identity{Group: group, User: s.store.admin}) || seen[group] {
			return nil, ErrInvalid
		}
		seen[group] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.tasksMu.Lock()
	var revoked []Identity
	for id := range s.revocations {
		revoked = append(revoked, id)
	}
	s.tasksMu.Unlock()
	for _, id := range revoked {
		if rosterAllows(groups, members, s.store.admin, id) && !rosterAllows(s.cfg.Groups, s.cfg.Members, s.store.admin, id) && !s.MemberCleanupReady(id.Group, strings.TrimPrefix(id.User, "wecom:")) {
			return nil, ErrUnavailable
		}
	}
	if err := persist(); err != nil {
		return nil, err
	}
	s.membersMu.Lock()
	s.cfg.Groups = append([]string(nil), groups...)
	s.cfg.Members = map[string][]string{}
	for group, users := range members {
		s.cfg.Members[group] = append([]string(nil), users...)
	}
	s.membersMu.Unlock()
	s.mu.Lock()
	for _, group := range groups {
		if _, loaded := s.history[group]; !loaded {
			s.history[group] = s.chat.History(group)
		}
	}
	s.mu.Unlock()
	var stopped []*groupTask
	s.store.mu.Lock()
	for code, p := range s.store.pending {
		if !rosterAllows(groups, members, s.store.admin, p.actor) {
			delete(s.store.pending, code)
		}
	}
	s.store.mu.Unlock()
	s.approvals.mu.Lock()
	for code, p := range s.approvals.pending {
		if !rosterAllows(groups, members, s.store.admin, p.actor) {
			delete(s.approvals.pending, code)
			p.decision <- false
		}
	}
	s.approvals.mu.Unlock()
	s.tasksMu.Lock()
	if s.revocations == nil {
		s.revocations = map[Identity][]*groupTask{}
	}
	for _, task := range s.tasks {
		if !rosterAllows(groups, members, s.store.admin, task.actor) {
			task.mu.Lock()
			if task.state != "publishing" {
				task.stopping = true
				task.state = "stopping"
			}
			task.cancel()
			task.mu.Unlock()
			stopped = append(stopped, task)
			s.revocations[task.actor] = append(s.revocations[task.actor], task)
		}
	}
	s.tasksMu.Unlock()
	s.tasksMu.Lock()
	for id, previous := range s.revocations {
		if !rosterAllows(groups, members, s.store.admin, id) {
			stopped = append(stopped, previous...)
		}
	}
	s.tasksMu.Unlock()
	return stopped, nil
}

func waitRevokedTasks(ctx context.Context, stopped []*groupTask) error {
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	for _, task := range stopped {
		select {
		case <-task.done:
			if task.cleanupErr != nil {
				return ErrUnavailable
			}
		case <-ctx.Done():
			return ErrUnavailable
		case <-timer.C:
			return ErrUnavailable
		}
	}
	return nil
}

func (s *Service) MemberCleanupReady(group, user string) bool {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	id := Identity{Group: group, User: "wecom:" + user}
	for _, task := range s.revocations[id] {
		select {
		case <-task.done:
			if task.cleanupErr != nil {
				return false
			}
		default:
			return false
		}
	}
	delete(s.revocations, id)
	return true
}

// Recovery never guesses that a failed cleanup succeeded. The native runner
// must be healthy, all relevant tasks finished, and no task copy may remain.
func (s *Service) RetryMemberCleanup(ctx context.Context, group, user string) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	ready, ok := s.runner.(interface{ Ready() error })
	if !ok || ready.Ready() != nil {
		return ErrUnavailable
	}
	entries, err := os.ReadDir(s.cfg.WorkDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "turn-") || strings.HasPrefix(entry.Name(), ".ccga-") {
			return ErrUnavailable
		}
	}
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for id, tasks := range s.revocations {
		if id.Group != group || user != "" && id.User != "wecom:"+user {
			continue
		}
		for _, task := range tasks {
			select {
			case <-task.done:
			default:
				return ErrUnavailable
			}
		}
	}
	for id := range s.revocations {
		if id.Group == group && (user == "" || id.User == "wecom:"+user) {
			delete(s.revocations, id)
		}
	}
	return nil
}
