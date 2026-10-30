package groupassistant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLongPublicReplyHistorySurvivesRestart(t *testing.T) {
	for name, reply := range map[string]string{"ascii": strings.Repeat("a", 9000), "chinese": strings.Repeat("中", 3000)} {
		t.Run(name, func(t *testing.T) {
			svc, store := testService(t, func(context.Context, *Snapshot, string) (string, error) { return reply, nil })
			cfg := svc.cfg
			cfg.StateDir = t.TempDir()
			service, err := NewService(cfg, store, svc.runner)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = service.Close() })
			got := service.Handle(context.Background(), memberMessage("alice", "long", "write a report"))
			if got.Text != reply {
				t.Fatal("visible reply was truncated")
			}
			if err := service.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := NewService(cfg, store, svc.runner)
			if err != nil {
				t.Fatal("cannot reload self-generated history:", err)
			}
			defer restarted.Close()
			for _, line := range restarted.chat.History("group") {
				if len(line) > 8000 || !utf8.ValidString(line) {
					t.Fatal("invalid history line", len(line))
				}
			}
		})
	}
}

func TestPartialDeleteRequestCannotPublishWholeFileDeletion(t *testing.T) {
	for _, text := range []string{"删除 a.txt 的第 2 行", "删除 a.txt的备份", "delete a.txt line 2", "删除 a.txt，然后创建备份"} {
		t.Run(text, func(t *testing.T) {
			svc, store := testService(t, func(_ context.Context, snap *Snapshot, _ string) (string, error) {
				return "done", os.Remove(filepath.Join(snap.Dir, "mine", "a.txt"))
			})
			svc.cfg.AutomaticRoutine = true
			svc.cfg.SoftwareDir = t.TempDir()
			actor := Identity{"group", "wecom:alice"}
			putTestFile(t, store, actor, "a.txt", "keep")
			response := svc.Handle(context.Background(), memberMessage("alice", "partial", text))
			data, err := store.Read(actor, FileRef{Name: "a.txt"})
			if err != nil || string(data) != "keep" || !strings.Contains(response.Text, "/confirm ") {
				t.Fatal("partial request authorized whole-file deletion", text, response.Text, err)
			}
		})
	}
}

func TestHistorySaveRejectsDataThatRestartWouldReject(t *testing.T) {
	state, err := newChatState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.SaveHistory("group", []string{strings.Repeat("a", 8001)}); err == nil {
		t.Fatal("saved oversized history line")
	}
	if err := state.SaveHistory("group", make([]string, 41)); err == nil {
		t.Fatal("saved too many history lines")
	}
	if len(state.History("group")) != 0 {
		t.Fatal("failed save changed history")
	}
}

func TestCompleteDirectDeletionStillAllowsTerminalPunctuation(t *testing.T) {
	for _, text := range []string{"删除 a.txt", "请删除 a.txt。", "delete a.txt!", "please delete a.txt."} {
		if !explicitFileIntent(text, "a.txt", true) {
			t.Fatal("direct deletion no longer recognized", text)
		}
	}
}
