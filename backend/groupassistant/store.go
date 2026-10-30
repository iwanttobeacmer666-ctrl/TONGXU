package groupassistant

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

var (
	ErrDenied      = errors.New("access denied")
	ErrInvalid     = errors.New("invalid request")
	ErrChanged     = errors.New("file changed; request confirmation again")
	ErrUnavailable = errors.New("isolated executor unavailable")
	ErrBusy        = errors.New("task queue full")
)

const MaxFileBytes = 8 << 20

// Identity must come from the authenticated platform callback, never model output.
type Identity struct{ Group, User string }
type FileRef struct{ Owner, Name string }
type Pending struct {
	Token         string
	Ref           FileRef
	Delete        bool
	Expires       time.Time
	actor         Identity
	before        [32]byte
	data          []byte
	management    bool
	configVersion string
}

// Store is the only component that writes persistent user files. Workers receive
// copies, never this root. os.Root confines symlinks/reparse points at the OS boundary.
type Store struct {
	mu      sync.Mutex
	root    *os.Root
	admin   string
	pending map[string]Pending
	members map[string][]string
	now     func() time.Time
}

func NewStore(dir, admin string) (*Store, error) {
	if strings.TrimSpace(admin) == "" {
		return nil, fmt.Errorf("store admin: %w", ErrInvalid)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("store directory: %w", err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	s := &Store{root: r, admin: admin, pending: map[string]Pending{}, members: map[string][]string{}, now: time.Now}
	if data, e := r.ReadFile(".ccga-members.json"); e == nil {
		if len(data) > 64<<10 || json.Unmarshal(data, &s.members) != nil || s.members == nil {
			r.Close()
			return nil, ErrInvalid
		}
	} else if !errors.Is(e, fs.ErrNotExist) {
		r.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.root.Close() }

func digestID(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func validIdentity(id Identity) bool {
	return strings.TrimSpace(id.Group) != "" && strings.TrimSpace(id.User) != "" &&
		len(id.Group) <= 256 && len(id.User) <= 256 && !strings.ContainsAny(id.Group+id.User, "\x00\r\n")
}
func cleanName(name string) (string, error) {
	if name == "" || len(name) > 240 || strings.ContainsAny(name, "\\:\x00") ||
		strings.HasPrefix(name, "/") || path.Clean(name) != name {
		return "", ErrInvalid
	}
	for _, p := range strings.Split(name, "/") {
		if p == "." || p == ".." || p == "" || strings.HasPrefix(p, ".ccga-") ||
			strings.TrimSpace(p) != p || strings.HasSuffix(p, ".") {
			return "", ErrInvalid
		}
		for _, c := range p {
			if c < 32 {
				return "", ErrInvalid
			}
		}
	}
	return name, nil
}
func (s *Store) scoped(id Identity, ref FileRef, write bool) (*os.Root, string, error) {
	if !validIdentity(id) {
		return nil, "", ErrDenied
	}
	if err := s.register(id); err != nil {
		return nil, "", err
	}
	name, err := cleanName(ref.Name)
	if err != nil {
		return nil, "", err
	}
	owner := ref.Owner
	if owner == "" || owner == "mine" {
		owner = id.User
	}
	if owner != id.User && owner != "public" && id.User != s.admin {
		return nil, "", ErrDenied
	}
	if write && owner == "public" && id.User != s.admin {
		return nil, "", ErrDenied
	}
	base := "groups/" + digestID(id.Group) + "/users/" + digestID(owner)
	if owner == "public" {
		base = "groups/" + digestID(id.Group) + "/public"
	}
	if err = s.root.MkdirAll(base, 0700); err != nil {
		return nil, "", fmt.Errorf("scope directory: %w", err)
	}
	r, err := s.root.OpenRoot(base)
	if err != nil {
		return nil, "", fmt.Errorf("open scope: %w", err)
	}
	return r, name, nil
}
func readLimited(r *os.Root, name string) ([]byte, error) {
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxFileBytes {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err == nil && len(data) > MaxFileBytes {
		err = ErrInvalid
	}
	return data, err
}
func (s *Store) Read(id Identity, ref FileRef) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, name, err := s.scoped(id, ref, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readLimited(r, name)
}
func (s *Store) List(id Identity, owner string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, err := s.scoped(id, FileRef{Owner: owner, Name: "listing"}, false)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var names []string
	err = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".ccga-") || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			names = append(names, p)
		}
		if len(names) > 1000 {
			return ErrInvalid
		}
		return nil
	})
	return names, err
}
func token() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (s *Store) prune() {
	for k, p := range s.pending {
		if !s.now().Before(p.Expires) {
			delete(s.pending, k)
		}
	}
}

// Put creates a new file immediately. Replacing an existing file only stages a
// single-use, actor-bound confirmation. It never silently truncates a file.
func (s *Store) Put(id Identity, ref FileRef, data []byte) (*Pending, error) {
	if len(data) > MaxFileBytes {
		return nil, ErrInvalid
	}
	return s.stage(id, ref, data, false, nil)
}
func (s *Store) Delete(id Identity, ref FileRef) (*Pending, error) {
	return s.stage(id, ref, nil, true, nil)
}
func (s *Store) stage(id Identity, ref FileRef, data []byte, del bool, expected *[32]byte) (*Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	r, name, err := s.scoped(id, ref, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	before, err := readLimited(r, name)
	if expected != nil && (err != nil || sha256.Sum256(before) != *expected) {
		return nil, ErrChanged
	}
	if errors.Is(err, fs.ErrNotExist) && !del {
		if e := r.MkdirAll(path.Dir(name), 0700); e != nil {
			return nil, e
		}
		f, e := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(data)
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			_ = r.Remove(name)
			return nil, errors.Join(e, closeErr)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	total := len(data)
	for _, p := range s.pending {
		total += len(p.data)
	}
	if len(s.pending) >= 30 || total > 16<<20 {
		return nil, ErrBusy
	}
	t, err := token()
	if err != nil {
		return nil, err
	}
	p := Pending{Token: t, Ref: ref, Delete: del, Expires: s.now().Add(5 * time.Minute),
		actor: id, before: sha256.Sum256(before), data: append([]byte(nil), data...)}
	s.pending[t] = p
	return &p, nil
}
func (s *Store) Confirm(id Identity, t string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	p, ok := s.pending[t]
	if !ok || p.actor != id || p.management {
		return ErrDenied
	}
	delete(s.pending, t) // consume before filesystem work; failed approvals cannot replay
	r, name, err := s.scoped(id, p.Ref, true)
	if err != nil {
		return err
	}
	defer r.Close()
	before, err := readLimited(r, name)
	if err != nil || sha256.Sum256(before) != p.before {
		return ErrChanged
	}
	if p.Delete {
		return r.Remove(name)
	}
	tmp, err := token()
	if err != nil {
		return err
	}
	tmp = ".ccga-replacement-" + tmp
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer r.Remove(tmp)
	_, err = f.Write(p.data)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	return r.Rename(tmp, name)
}
func (s *Store) Cancel(id Identity, t string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	p, ok := s.pending[t]
	if !ok || p.actor != id {
		return ErrDenied
	}
	delete(s.pending, t)
	return nil
}
