package groupassistant

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type GroupSettings struct{ Model, Effort string }

const maxHistoryEntries = 40
const maxHistoryLineBytes = 8000

type Conversation struct {
	Identity                Identity
	ThreadID, Model, Effort string
	Context                 *core.ContextUsage
	ObservedAt              time.Time
}
type chatData struct {
	Version       int
	Settings      map[string]GroupSettings
	Conversations map[string]Conversation
	History       map[string][]string
	ThreadOwners  map[string]Identity `json:",omitempty"`
}
type ChatState struct {
	mu   sync.Mutex
	root *os.Root
	data chatData
}

func newChatState(dir string) (*ChatState, error) {
	s := &ChatState{data: chatData{Version: 1, Settings: map[string]GroupSettings{}, Conversations: map[string]Conversation{}, History: map[string][]string{}, ThreadOwners: map[string]Identity{}}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	s.root = root
	f, err := root.Open("chat-state.json")
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 8<<20+1))
	decoder.DisallowUnknownFields()
	var loaded chatData
	if err = decoder.Decode(&loaded); err != nil {
		root.Close()
		return nil, err
	}
	s.data = loaded
	if s.data.ThreadOwners == nil {
		s.data.ThreadOwners = map[string]Identity{}
	}
	if decoder.Decode(&struct{}{}) != io.EOF || s.data.Version != 1 || s.data.Settings == nil || s.data.Conversations == nil || s.data.History == nil || len(s.data.Settings) > 16 || len(s.data.History) > 16 || len(s.data.Conversations) > 80 {
		root.Close()
		return nil, ErrInvalid
	}
	threads := map[string]bool{}
	for key, item := range s.data.Conversations {
		if !validIdentity(item.Identity) || key != conversationKey(item.Identity) || !validThreadID(item.ThreadID) {
			root.Close()
			return nil, ErrInvalid
		}
		if threads[item.ThreadID] {
			root.Close()
			return nil, ErrDenied
		}
		threads[item.ThreadID] = true
		if owner, ok := s.data.ThreadOwners[item.ThreadID]; ok && owner != item.Identity {
			root.Close()
			return nil, ErrDenied
		}
		s.data.ThreadOwners[item.ThreadID] = item.Identity
	}
	if len(s.data.ThreadOwners) > 4096 {
		root.Close()
		return nil, ErrInvalid
	}
	for thread, owner := range s.data.ThreadOwners {
		if !validThreadID(thread) || !validIdentity(owner) {
			root.Close()
			return nil, ErrInvalid
		}
	}
	for _, history := range s.data.History {
		if len(history) > maxHistoryEntries {
			root.Close()
			return nil, ErrInvalid
		}
		for _, line := range history {
			if len(line) > maxHistoryLineBytes {
				root.Close()
				return nil, ErrInvalid
			}
		}
	}
	return s, nil
}
func (s *ChatState) Close() error {
	if s.root != nil {
		return s.root.Close()
	}
	return nil
}
func conversationKey(id Identity) string { return digestID(id.Group + "\x00" + id.User) }
func validThreadID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r < '!' || r > '~' || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func (s *ChatState) change(update func(*chatData) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	var next chatData
	if err = json.Unmarshal(data, &next); err != nil {
		return err
	}
	if err = update(&next); err != nil {
		return err
	}
	data, err = json.Marshal(next)
	if err != nil || len(data) > 8<<20 {
		return ErrInvalid
	}
	if s.root != nil {
		id, err := token()
		if err != nil {
			return err
		}
		tmp := ".chat-state-" + id
		defer s.root.Remove(tmp)
		if err = s.root.WriteFile(tmp, data, 0600); err != nil {
			return err
		}
		if err = s.root.Rename(tmp, "chat-state.json"); err != nil {
			return err
		}
	}
	s.data = next
	return nil
}
func (s *ChatState) Settings(group string) GroupSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Settings[group]
}
func (s *ChatState) SetSettings(group string, value GroupSettings) error {
	return s.change(func(data *chatData) error {
		if len(data.Settings) >= 16 {
			if _, ok := data.Settings[group]; !ok {
				return ErrDenied
			}
		}
		data.Settings[group] = value
		return nil
	})
}
func (s *ChatState) Conversation(id Identity) Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.data.Conversations[conversationKey(id)]
	if result.Context != nil {
		copy := *result.Context
		result.Context = &copy
	}
	return result
}
func (s *ChatState) SaveConversation(id Identity, value Conversation) error {
	if !validIdentity(id) || !validThreadID(value.ThreadID) || value.Identity != id {
		return ErrDenied
	}
	return s.change(func(data *chatData) error {
		key := conversationKey(id)
		if data.ThreadOwners == nil {
			data.ThreadOwners = map[string]Identity{}
		}
		if owner, exists := data.ThreadOwners[value.ThreadID]; exists && owner != id {
			return ErrDenied
		}
		if len(data.ThreadOwners) >= 4096 {
			if _, exists := data.ThreadOwners[value.ThreadID]; !exists {
				return ErrBusy
			}
		}
		if len(data.Conversations) >= 80 {
			if _, ok := data.Conversations[key]; !ok {
				return ErrDenied
			}
		}
		// A native thread may never be rebound to another member, including owner.
		for otherKey, item := range data.Conversations {
			if otherKey != key && item.ThreadID == value.ThreadID {
				return ErrDenied
			}
		}
		data.Conversations[key] = value
		data.ThreadOwners[value.ThreadID] = id
		return nil
	})
}
func (s *ChatState) Reset(id Identity) error {
	return s.change(func(data *chatData) error { delete(data.Conversations, conversationKey(id)); return nil })
}
func (s *ChatState) History(group string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.data.History[group]...)
}
func (s *ChatState) SaveHistory(group string, history []string) error {
	if len(history) > maxHistoryEntries {
		return fmt.Errorf("history exceeds limit")
	}
	for _, line := range history {
		if len(line) > maxHistoryLineBytes {
			return fmt.Errorf("history line exceeds limit")
		}
	}
	return s.change(func(data *chatData) error { data.History[group] = append([]string(nil), history...); return nil })
}
