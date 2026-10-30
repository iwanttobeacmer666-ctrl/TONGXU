package groupassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

var ErrInventoryLimit = errors.New("administrator inventory exceeds 100000 entries")

// Administrator reads do not inherit the worker upload/snapshot memory limit.
// A single pass hashes the file and retains only the requested 64-KiB window.
type chunkWindow struct {
	offset int64
	data   []byte
}

func (w *chunkWindow) Write(p []byte) (int, error) {
	n := len(p)
	if w.offset >= int64(n) {
		w.offset -= int64(n)
		return n, nil
	}
	p = p[w.offset:]
	w.offset = 0
	w.data = append(w.data, p[:min(len(p), (64<<10)-len(w.data))]...)
	return n, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Refuse links in every component; os.Root additionally confines all operations
// to the opened scope even if an external process changes a path concurrently.
func regularPath(r *os.Root, name string) (fs.FileInfo, error) {
	parts := strings.Split(name, "/")
	var info fs.FileInfo
	for i := range parts {
		var err error
		info, err = r.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return nil, ErrDenied
		}
	}
	return info, nil
}

func (s *Store) adminScope(id Identity, ref FileRef, write bool) (*os.Root, string, error) {
	r, name, err := s.scoped(id, ref, write)
	if err != nil {
		return nil, "", err
	}
	owner := ref.Owner
	if owner == "" || owner == "mine" {
		owner = id.User
	}
	base := "groups/" + digestID(id.Group) + "/users/" + digestID(owner)
	if owner == "public" {
		base = "groups/" + digestID(id.Group) + "/public"
	}
	if _, err := regularPath(s.root, base); err != nil {
		r.Close()
		return nil, "", err
	}
	return r, name, nil
}

func inspectFile(ctx context.Context, r *os.Root, name string, offset int64, chunk bool) (ManagedFile, []byte, error) {
	info, err := regularPath(r, name)
	if err != nil {
		return ManagedFile{}, nil, err
	}
	if !info.Mode().IsRegular() || offset < 0 || offset > info.Size() {
		return ManagedFile{}, nil, ErrInvalid
	}
	f, err := r.Open(name)
	if err != nil {
		return ManagedFile{}, nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return ManagedFile{}, nil, err
	}
	if !os.SameFile(info, before) {
		return ManagedFile{}, nil, ErrChanged
	}
	h := sha256.New()
	w := &chunkWindow{offset: offset}
	var dst io.Writer = h
	if chunk {
		dst = io.MultiWriter(h, w)
	}
	n, err := io.CopyBuffer(dst, contextReader{ctx, f}, make([]byte, 32<<10))
	if err != nil {
		return ManagedFile{}, nil, err
	}
	after, err := f.Stat()
	if err != nil {
		return ManagedFile{}, nil, err
	}
	current, err := regularPath(r, name)
	if err != nil || !os.SameFile(before, current) || n != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !after.ModTime().Equal(current.ModTime()) {
		return ManagedFile{}, nil, ErrChanged
	}
	return ManagedFile{Name: name, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, w.data, nil
}

// walkManaged never follows links. Strict previews reject unsupported entries
// rather than silently omitting something from a requested complete clear.
func walkManaged(ctx context.Context, r *os.Root, documents, strict bool) ([]ManagedFile, error) {
	var items []ManagedFile
	err := fs.WalkDir(r.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		if documents && strings.HasPrefix(d.Name(), ".ccga-") {
			if strict {
				return ErrBusy
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := regularPath(r, name)
		if err != nil {
			if strict {
				return err
			}
			if !documents {
				items = append(items, ManagedFile{Name: name, Blocked: true})
				if len(items) > 100000 {
					return ErrInventoryLimit
				}
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if documents {
			if _, err := cleanName(name); err != nil {
				return err
			}
		}
		item := ManagedFile{Name: name, Directory: info.IsDir()}
		if !item.Directory {
			if strict {
				item, _, err = inspectFile(ctx, r, name, 0, false)
			} else {
				item.Bytes = info.Size()
			}
			if err != nil {
				return err
			}
		}
		items = append(items, item)
		if len(items) > 100000 {
			return ErrInventoryLimit
		}
		return nil
	})
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, err
}

func (s *Store) adminDocumentRead(ctx context.Context, id Identity, user, name string, offset int) (DocumentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, name, err := s.adminScope(id, FileRef{Owner: user, Name: name}, false)
	if err != nil {
		return DocumentResult{}, err
	}
	defer r.Close()
	item, data, err := inspectFile(ctx, r, name, int64(offset), true)
	return DocumentResult{Name: name, Total: int(item.Bytes), NextOffset: offset + len(data), SHA256: item.SHA256, Data: data}, err
}

func (s *Store) adminDocumentList(ctx context.Context, id Identity, user string, offset, limit int) (DocumentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, _, err := s.adminScope(id, FileRef{Owner: user, Name: "listing"}, false)
	if err != nil {
		return DocumentResult{}, err
	}
	defer r.Close()
	items, err := walkManaged(ctx, r, true, false)
	if err != nil {
		return DocumentResult{}, err
	}
	files := items[:0]
	for _, item := range items {
		if !item.Directory {
			files = append(files, item)
		}
	}
	if offset > len(files) {
		return DocumentResult{}, ErrInvalid
	}
	end := offset + min(limit, len(files)-offset)
	out := DocumentResult{Total: len(files), NextOffset: end}
	for _, file := range files[offset:end] {
		item, _, err := inspectFile(ctx, r, file.Name, 0, false)
		if err != nil {
			return DocumentResult{}, err
		}
		out.Files = append(out.Files, DocumentEntry{Name: item.Name, SHA256: item.SHA256, Bytes: int(item.Bytes)})
	}
	return out, nil
}

func (s *Store) stageAdminDelete(ctx context.Context, id Identity, ref FileRef, version string) (*Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if len(s.pending) >= 30 {
		return nil, ErrBusy
	}
	r, name, err := s.adminScope(id, ref, true)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	item, _, err := inspectFile(ctx, r, name, 0, false)
	if err != nil {
		return nil, err
	}
	t, err := token()
	if err != nil {
		return nil, err
	}
	p := Pending{Token: t, Ref: ref, Delete: true, Expires: s.now().Add(5 * time.Minute), actor: id, management: true, configVersion: version}
	before, _ := hex.DecodeString(item.SHA256)
	copy(p.before[:], before)
	s.pending[t] = p
	return &p, nil
}

func (s *Store) confirmAdminDelete(ctx context.Context, id Identity, user, name, confirmation, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	p, ok := s.pending[confirmation]
	if !ok || p.actor != id || !p.management || p.configVersion != version || !p.Delete || p.Ref.Owner != user || name != "" && p.Ref.Name != name {
		return ErrDenied
	}
	delete(s.pending, confirmation)
	r, file, err := s.adminScope(id, p.Ref, true)
	if err != nil {
		return err
	}
	defer r.Close()
	item, _, err := inspectFile(ctx, r, file, 0, false)
	if err != nil || item.SHA256 != hex.EncodeToString(p.before[:]) {
		return errors.Join(ErrChanged, err)
	}
	return r.Remove(file)
}
