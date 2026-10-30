package codex

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DeleteNativeThreads uses the native metadata-aware operation. Removing a
// rollout file alone leaves turns, indexes and derived threads behind.
func (s *appServerSession) DeleteNativeThreads(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if id == "" || len(id) > 128 || strings.ContainsAny(id, "\\/\x00\r\n") {
			return errors.New("invalid native thread ID")
		}
	}
	persisted, err := s.persistedThreadIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !persisted[id] {
			found, err := s.hasThreadRollout(id)
			if err != nil {
				return err
			}
			if !found {
				continue
			} // Empty or already deleted, proven absent in both stores.
		}
		timeout, err := controlTimeout(ctx)
		if err != nil {
			return err
		}
		if err := s.requestWithTimeout("thread/delete", map[string]any{"threadId": id}, nil, timeout); err != nil {
			return err
		}
	}
	return nil
}

func (s *appServerSession) persistedThreadIDs(ctx context.Context) (map[string]bool, error) {
	ids := map[string]bool{}
	for _, archived := range []bool{false, true} {
		cursor := ""
		for page := 0; page < 100; page++ {
			timeout, err := controlTimeout(ctx)
			if err != nil {
				return nil, err
			}
			var result struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
				NextCursor *string `json:"nextCursor"`
			}
			params := map[string]any{"limit": 100, "archived": archived, "modelProviders": []string{}, "useStateDbOnly": true, "sourceKinds": []string{"cli", "vscode", "exec", "appServer", "subAgent", "subAgentReview", "subAgentCompact", "subAgentThreadSpawn", "subAgentOther", "unknown"}}
			if cursor != "" {
				params["cursor"] = cursor
			}
			if err = s.requestWithTimeout("thread/list", params, &result, timeout); err != nil {
				return nil, err
			}
			for _, thread := range result.Data {
				ids[thread.ID] = true
			}
			if result.NextCursor == nil || *result.NextCursor == "" {
				break
			}
			if *result.NextCursor == cursor || page == 99 {
				return nil, errors.New("native thread pagination did not finish")
			}
			cursor = *result.NextCursor
		}
	}
	return ids, nil
}

func (s *appServerSession) hasThreadRollout(id string) (bool, error) {
	if !filepath.IsAbs(s.codexHome) {
		return false, errors.New("dedicated Codex home required for deletion verification")
	}
	found := false
	for _, sub := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(s.codexHome, sub)
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return false, err
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("linked native transcript path")
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), "-"+id+".jsonl") {
				found = true
			}
			return nil
		})
		if err != nil {
			return false, err
		}
	}
	return found, nil
}
