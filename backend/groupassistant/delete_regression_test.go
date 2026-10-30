package groupassistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNaturalOwnFileDeletionProvidesMXCCompatibleInstructions(t *testing.T) {
	service, store := testService(t, func(_ context.Context, snap *Snapshot, prompt string) (string, error) {
		if !strings.Contains(prompt, "[IO.File]::Delete") || !strings.Contains(prompt, "Remove-Item") {
			t.Fatal("native task lacks the sandbox-compatible file deletion instructions")
		}
		if _, err := os.Stat(filepath.Join(snap.Dir, "members")); !os.IsNotExist(err) {
			t.Fatal("ordinary member received another member's workspace")
		}
		return "deleted from working copy", os.Remove(filepath.Join(snap.Dir, "mine", "a.txt"))
	})
	service.cfg.AutomaticRoutine = true
	service.cfg.SoftwareDir = t.TempDir()
	alice, bob := Identity{"group", "wecom:alice"}, Identity{"group", "wecom:bob"}
	putTestFile(t, store, alice, "a.txt", "alice fixture")
	putTestFile(t, store, bob, "a.txt", "bob fixture")
	response := service.Handle(context.Background(), memberMessage("alice", "natural-delete", "删除 a.txt"))
	if strings.Contains(response.Text, "/confirm ") {
		t.Fatal("explicit deletion unexpectedly requires confirmation", response.Text)
	}
	if _, err := store.Read(alice, FileRef{Name: "a.txt"}); !os.IsNotExist(err) {
		t.Fatalf("own deletion not published: %s %v", response.Text, err)
	}
	if data, err := store.Read(bob, FileRef{Name: "a.txt"}); err != nil || string(data) != "bob fixture" {
		t.Fatalf("another member's file changed: %q %v", data, err)
	}
}
