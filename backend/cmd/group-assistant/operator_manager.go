package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chenhg5/cc-connect/groupassistant"
)

type memberRecord struct {
	ID      string
	Enabled bool
	State   string `json:",omitempty"`
}
type groupRecord struct {
	ID      string
	Enabled bool
	Members []memberRecord
	State   string `json:",omitempty"`
}
type manageRequest struct {
	Action, Version, Group, User, Task string
	Name, Confirm, Scope, FileVersion  string
	Offset, Limit                      int
	Names                              []string `json:",omitempty"`
}
type manageReply struct {
	Success, Online, Applied, CleanupReady bool
	Version, Message, Tasks                string
	Owner                                  string                              `json:",omitempty"`
	Status                                 *localServiceStatus                 `json:",omitempty"`
	Code                                   string                              `json:",omitempty"`
	Groups                                 []groupRecord                       `json:",omitempty"`
	TaskItems                              []groupassistant.TaskView           `json:",omitempty"`
	Documents                              *groupassistant.DocumentResult      `json:",omitempty"`
	DeletePreview                          *groupassistant.MemberDeletePreview `json:",omitempty"`
	FileOperation                          *groupassistant.FileOperationView   `json:",omitempty"`
}

const maxManageRequest = 64 << 10

type operatorManager struct {
	mu       sync.Mutex
	path     string
	service  *groupassistant.Service
	owner    string
	platform interface{ ConnectionStatus() (string, time.Time) }
	shutdown context.CancelFunc
	stopping bool
	started  time.Time
}

var errNoOperatorServer = errors.New("no operator server")

func runManagement(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("operator settings path must be absolute")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxManageRequest+1))
	if err != nil {
		return err
	}
	req, err := decodeManage(input)
	if err != nil {
		return errors.New("invalid local management request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	reply, err := operatorRemote(ctx, path, req)
	if errors.Is(err, errNoOperatorServer) {
		if err = operatorOfflineSafe(); err != nil {
			return errors.New("an instance is running or cannot be checked; no offline change")
		}
		// No online request was sent. Both locks must be acquired before even
		// considering offline writes; a busy/starting/uncertain instance is not offline.
		release, e := operatorLock(filepath.Join(filepath.Dir(path), "operator.lock"))
		if e != nil {
			return errors.New("configuration busy; no change made")
		}
		defer release()
		releaseService, e := operatorLock(filepath.Join(filepath.Dir(path), "service.lock"))
		if e != nil {
			return errors.New("service starting or unavailable; no offline fallback")
		}
		defer releaseService()
		manager := operatorManager{path: path}
		reply = manager.handle(ctx, req)
		err = nil
	}
	if err != nil {
		return errors.New("local service unavailable or result uncertain; refresh, do not replay the operation")
	}
	return json.NewEncoder(os.Stdout).Encode(reply)
}

func readOperator(path string) (map[string]json.RawMessage, string, string, []groupRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", "", nil, errors.New("operator settings unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 262145))
	if err != nil || len(data) > 262144 {
		return nil, "", "", nil, errors.New("operator settings unavailable")
	}
	var raw map[string]json.RawMessage
	var owner string
	var groups []groupRecord
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(raw["Owner"], &owner) != nil || json.Unmarshal(raw["Groups"], &groups) != nil || len(groups) == 0 || len(groups) > 16 {
		return nil, "", "", nil, errors.New("invalid operator settings")
	}
	if strings.TrimSpace(owner) == "" || len(owner) > 250 || strings.ContainsAny(owner, "\x00\r\n") {
		return nil, "", "", nil, errors.New("invalid owner")
	}
	seen := map[string]bool{}
	for _, group := range groups {
		if strings.TrimSpace(group.ID) == "" || len(group.ID) > 256 || strings.ContainsAny(group.ID, "\x00\r\n") || seen[group.ID] {
			return nil, "", "", nil, errors.New("invalid group")
		}
		seen[group.ID] = true
		if !validLifecycle(group.State, group.Enabled) {
			return nil, "", "", nil, errors.New("invalid group lifecycle")
		}
		var users []string
		for _, user := range group.Members {
			if !validLifecycle(user.State, user.Enabled) {
				return nil, "", "", nil, errors.New("invalid member lifecycle")
			}
			users = append(users, user.ID)
		}
		if groupassistant.ValidateMembers(owner, []string{group.ID}, map[string][]string{group.ID: users}) != nil {
			return nil, "", "", nil, errors.New("invalid enrollment")
		}
	}
	sum := sha256.Sum256(data)
	return raw, hex.EncodeToString(sum[:]), owner, groups, nil
}

func (m *operatorManager) handle(ctx context.Context, req manageRequest) manageReply {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := manageReply{Online: m.service != nil, CleanupReady: true}
	raw, version, owner, groups, err := readOperator(m.path)
	if err != nil {
		out.Message = "设置文件不可用，请先用原入口完成首次配置。"
		return out
	}
	if m.owner != "" && owner != m.owner {
		out.Message = "管理员与运行实例不一致，拒绝修改。"
		return out
	}
	out.Version = version
	out.Owner = owner
	out.Groups = groups
	if req.Action == "status" || req.Action == "service-stop" {
		out.Groups = nil
		out.CleanupReady = false
		if req.Action == "service-stop" {
			if m.service == nil || m.shutdown == nil {
				out.Message = "服务未运行或不支持停止。"
				return out
			}
			if req.Version == "" || req.Version != version {
				out.Code = "version_conflict"
				out.Message = "配置版本已变化，请刷新后重试。"
				return out
			}
			m.stopping = true
			out.Message = "停止请求已接受；请等待服务退出，尚未确认临时清理完成。"
		}
		out.Status = &localServiceStatus{Service: "offline", Platform: "stopped", Agent: "unchecked", Started: m.started}
		if m.service != nil {
			out.Status.Service = "running"
			if m.stopping {
				out.Status.Service = "stopping"
			}
			out.Status.Agent = "unavailable"
			if m.service.AgentReady() {
				out.Status.Agent = "ready"
			}
			out.Status.Platform = "unknown"
			if m.platform != nil {
				out.Status.Platform, out.Status.ConnectedAt = m.platform.ConnectionStatus()
			}
		}
		out.Success = true
		return out
	}
	if m.stopping && req.Action != "list" && req.Action != "tasks" && req.Action != "task-detail" {
		out.Message = "服务正在停止，请等待退出。"
		return out
	}
	if handled := m.handleExtended(ctx, req, raw, owner, groups, &out); handled {
		return out
	}
	out.Message = "不支持的操作。"
	return out
}

func decodeManage(data []byte) (manageRequest, error) {
	var req manageRequest
	if len(data) > maxManageRequest {
		return req, errors.New("oversized request")
	}
	// .NET Framework's redirected stdin writer may emit a UTF-8 preamble
	// when StandardInput is initialized (PowerShell 5.1). Ignore only that
	// leading preamble; size, field whitelist and single-object checks remain.
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return req, errors.New("extra request")
	}
	return req, nil
}

func atomicOperatorWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".operator-update-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = f.Write(data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	return replaceOperator(tmp, path)
}
