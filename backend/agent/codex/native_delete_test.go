package codex

import (
	"context"
	"testing"
)

func TestNativeDeleteUsesMetadataAwareRPCWithoutInference(t *testing.T) {
	s := &appServerSession{ctx: context.Background(), codexHome: t.TempDir()}
	var deleted []string
	persisted := map[string]bool{"thread-a": true, "thread-b": true}
	s.stdin = &controlReplyWriter{s: s, reply: func(method string, params map[string]any) any {
		if method == "thread/list" {
			var data []map[string]string
			for id := range persisted {
				data = append(data, map[string]string{"id": id})
			}
			return map[string]any{"data": data, "nextCursor": nil}
		}
		if method != "thread/delete" {
			t.Fatal("unexpected inference or archive", method)
		}
		deleted = append(deleted, params["threadId"].(string))
		delete(persisted, params["threadId"].(string))
		return map[string]any{}
	}}
	if err := s.DeleteNativeThreads(context.Background(), []string{"thread-a", "thread-b"}); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Fatal(deleted)
	}
	if err := s.DeleteNativeThreads(context.Background(), []string{"thread-a", "thread-b"}); err != nil {
		t.Fatal("already absent threads must be retryable", err)
	}
	if err := s.DeleteNativeThreads(context.Background(), []string{"../escape"}); err == nil {
		t.Fatal("invalid thread ID accepted")
	}
	if len(deleted) != 2 {
		t.Fatal("invalid ID reached native server")
	}
}
