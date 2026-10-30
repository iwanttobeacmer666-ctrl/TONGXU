package groupassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type MemberDeletePreview struct {
	Token           string
	Actors          []Identity
	Files, Bytes    int64
	Threads         int
	Expires         time.Time
	PublicPreserved bool
}
type memberDeletePlan struct {
	MemberDeletePreview
	digest string
}

func (s *ChatState) threadIDs(id Identity) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := map[string]bool{}
	for thread, owner := range s.data.ThreadOwners {
		if owner == id {
			set[thread] = true
		}
	}
	if conversation := s.data.Conversations[conversationKey(id)]; conversation.ThreadID != "" {
		set[conversation.ThreadID] = true
	}
	var ids []string
	for thread := range set {
		ids = append(ids, thread)
	}
	sort.Strings(ids)
	return ids
}

func (s *ChatState) forgetMember(id Identity) error {
	return s.change(func(data *chatData) error {
		delete(data.Conversations, conversationKey(id))
		for thread, owner := range data.ThreadOwners {
			if owner == id {
				delete(data.ThreadOwners, thread)
			}
		}
		return nil
	})
}

// Fingerprints are bounded by context and stream file contents without loading
// installations into memory. Links are rejected rather than followed.
func fingerprintTree(ctx context.Context, root string, out io.Writer) (files, bytes int64, err error) {
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, 0, ErrInvalid
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()
	err = fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrDenied
		}
		if d.IsDir() {
			return nil
		}
		f, err := r.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			return ErrInvalid
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		files++
		bytes += n
		record, _ := json.Marshal([]any{name, n, hex.EncodeToString(h.Sum(nil))})
		_, err = out.Write(record)
		return err
	})
	return
}

func memberBase(id Identity) string {
	return "groups/" + digestID(id.Group) + "/users/" + digestID(id.User)
}

func (s *Service) memberFingerprint(ctx context.Context, actors []Identity) (string, int64, int64, int, error) {
	// Existing task copies use a shared random-name temporary root. With no
	// durable ownership proof for remnants, never report a member fully erased
	// while such data remains. An active task can be waited for or stopped.
	entries, err := os.ReadDir(s.cfg.WorkDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", 0, 0, 0, err
	}
	if len(entries) > 0 {
		return "", 0, 0, 0, ErrBusy
	}
	h := sha256.New()
	var files, bytes int64
	threads := 0
	for _, id := range actors {
		if !validIdentity(id) || id.User == s.store.admin || !strings.HasPrefix(id.User, "wecom:") || s.actorEnabled(id) || !s.MemberCleanupReady(id.Group, strings.TrimPrefix(id.User, "wecom:")) {
			return "", 0, 0, 0, ErrDenied
		}
		data, _ := json.Marshal(id)
		h.Write(data)
		s.store.mu.Lock()
		n, size, err := fingerprintTree(ctx, filepath.Join(s.store.root.Name(), filepath.FromSlash(memberBase(id))), h)
		s.store.mu.Unlock()
		if err != nil {
			return "", 0, 0, 0, err
		}
		files += n
		bytes += size
		if s.cfg.SoftwareDir != "" {
			n, size, err = fingerprintTree(ctx, filepath.Join(s.cfg.SoftwareDir, digestID(id.Group), digestID(id.User)), h)
			if err != nil {
				return "", 0, 0, 0, err
			}
			files += n
			bytes += size
		}
		ids := s.chat.threadIDs(id)
		data, _ = json.Marshal(ids)
		h.Write(data)
		threads += len(ids)
	}
	return hex.EncodeToString(h.Sum(nil)), files, bytes, threads, nil
}

func (s *Service) PreviewMemberDelete(ctx context.Context, owner string, actors []Identity) (MemberDeletePreview, error) {
	if owner != s.store.admin || len(actors) == 0 || len(actors) > 16 {
		return MemberDeletePreview{}, ErrDenied
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	seen := map[Identity]bool{}
	for _, actor := range actors {
		if seen[actor] {
			return MemberDeletePreview{}, ErrInvalid
		}
		seen[actor] = true
	}
	digest, files, bytes, threads, err := s.memberFingerprint(ctx, actors)
	if err != nil {
		return MemberDeletePreview{}, err
	}
	if s.deletePlans == nil {
		s.deletePlans = map[string]memberDeletePlan{}
	}
	for key, plan := range s.deletePlans {
		if time.Now().After(plan.Expires) {
			delete(s.deletePlans, key)
		}
	}
	if len(s.deletePlans) >= 20 {
		return MemberDeletePreview{}, ErrBusy
	}
	code, err := token()
	if err != nil {
		return MemberDeletePreview{}, err
	}
	preview := MemberDeletePreview{Token: code, Actors: append([]Identity(nil), actors...), Files: files, Bytes: bytes, Threads: threads,
		Expires: time.Now().Add(5 * time.Minute), PublicPreserved: true}
	s.deletePlans[code] = memberDeletePlan{preview, digest}
	return preview, nil
}

// persistStage durably marks the disabled memberships before deleting anything.
// finish removes enrollment only after all private cleanup has succeeded.
func (s *Service) DeleteMembers(ctx context.Context, owner, code string, actors []Identity, persistStage func(string) error, finish func() error) (err error) {
	if owner != s.store.admin || persistStage == nil || finish == nil {
		return ErrDenied
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	plan, ok := s.deletePlans[code]
	delete(s.deletePlans, code)
	if !ok || !time.Now().Before(plan.Expires) {
		return ErrChanged
	}
	want, _ := json.Marshal(plan.Actors)
	got, _ := json.Marshal(actors)
	if string(want) != string(got) {
		return ErrDenied
	}
	digest, _, _, _, err := s.memberFingerprint(ctx, actors)
	if err != nil {
		return err
	}
	if digest != plan.digest {
		return ErrChanged
	}
	if err := persistStage("deleting"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, persistStage("delete_failed"))
		}
	}()
	for _, id := range actors {
		threads := s.chat.threadIDs(id)
		if len(threads) > 0 {
			cleaner, ok := s.runner.(ThreadCleaner)
			if !ok {
				return ErrUnavailable
			}
			if err := cleaner.DeleteThreads(ctx, s.cfg.WorkDir, threads); err != nil {
				return err
			}
		}
		s.store.mu.Lock()
		err := s.store.root.RemoveAll(memberBase(id))
		if err == nil {
			for key, p := range s.store.pending {
				if p.actor == id || p.actor.Group == id.Group && p.Ref.Owner == id.User {
					delete(s.store.pending, key)
				}
			}
			var users []string
			for _, user := range s.store.members[id.Group] {
				if user != id.User {
					users = append(users, user)
				}
			}
			next := make(map[string][]string, len(s.store.members))
			for group, members := range s.store.members {
				next[group] = members
			}
			next[id.Group] = users
			data, encodeErr := json.Marshal(next)
			if encodeErr != nil {
				err = encodeErr
			} else {
				name, tokenErr := token()
				if tokenErr != nil {
					err = tokenErr
				} else {
					name = ".ccga-members-delete-" + name
					err = s.store.root.WriteFile(name, data, 0600)
					if err == nil {
						err = s.store.root.Rename(name, ".ccga-members.json")
					}
					_ = s.store.root.Remove(name)
					if err == nil {
						s.store.members = next
					}
				}
			}
		}
		s.store.mu.Unlock()
		if err != nil {
			return err
		}
		if s.cfg.SoftwareDir != "" {
			r, err := os.OpenRoot(s.cfg.SoftwareDir)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if r != nil {
				err = r.RemoveAll(digestID(id.Group) + "/" + digestID(id.User))
				closeErr := r.Close()
				if err != nil || closeErr != nil {
					return errors.Join(err, closeErr)
				}
			}
		}
		if err := s.chat.forgetMember(id); err != nil {
			return err
		}
		s.tasksMu.Lock()
		var recent []*groupTask
		for _, task := range s.recentTasks {
			if task.actor != id {
				recent = append(recent, task)
			}
		}
		s.recentTasks = recent
		s.tasksMu.Unlock()
	}
	return finish()
}
