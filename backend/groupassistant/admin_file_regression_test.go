package groupassistant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminDocumentsBeyondWorkerSizeAndCountLimits(t *testing.T) {
	s, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return "", nil })
	owner := Identity{"group", "wecom:owner"}
	putTestFile(t, store, owner, "seed.txt", "seed")
	base := filepath.Join(store.root.Name(), filepath.FromSlash(memberBase(owner)))
	f, err := os.Create(filepath.Join(base, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxFileBytes + 65537); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	read, err := s.AdminDocuments(owner.User, owner.Group, owner.User, "document-read", "large.bin", "", "", MaxFileBytes, 0)
	if err != nil || len(read.Data) != 64<<10 || read.Total != MaxFileBytes+65537 {
		t.Fatalf("administrator read still inherits worker limit: total=%d bytes=%d err=%v", read.Total, len(read.Data), err)
	}
	for i := 0; i < 1001; i++ {
		if err = os.WriteFile(filepath.Join(base, fmt.Sprintf("f-%04d.txt", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.AdminDocuments(owner.User, owner.Group, owner.User, "documents", "", "", "", 1000, 10)
	if err != nil || page.Total != 1003 || len(page.Files) != 3 {
		t.Fatalf("administrator list still inherits worker limit: %+v %v", page, err)
	}
	if _, err := store.Read(owner, FileRef{Name: "large.bin"}); err == nil {
		t.Fatal("worker size limit was weakened")
	}
}
