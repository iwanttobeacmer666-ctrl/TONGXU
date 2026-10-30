package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chenhg5/cc-connect/groupassistant"
)

func validLifecycle(state string, enabled bool) bool {
	if state == "" {
		return true
	}
	if enabled {
		return state == "enabled"
	}
	switch state {
	case "disabled", "disabling", "cleanup_failed", "deleting", "delete_failed":
		return true
	}
	return false
}

func activeRoster(groups []groupRecord) ([]string, map[string][]string) {
	var ids []string
	members := map[string][]string{}
	for _, group := range groups {
		if !group.Enabled {
			continue
		}
		ids = append(ids, group.ID)
		for _, user := range group.Members {
			if user.Enabled {
				members[group.ID] = append(members[group.ID], user.ID)
			}
		}
	}
	return ids, members
}

func saveManagedRoster(path string, raw map[string]json.RawMessage, owner string, groups []groupRecord, expected *string, out *manageReply) error {
	for i := range groups {
		if groups[i].Members == nil {
			groups[i].Members = []memberRecord{}
		}
	}
	ids, members := activeRoster(groups)
	enrollment, _ := json.Marshal(members)
	if len(groups) == 0 || len(groups) > 16 || len(enrollment) > 48<<10 || groupassistant.ValidateMembers(owner, ids, members) != nil {
		return errors.New("invalid or oversized roster")
	}
	_, current, _, _, err := readOperator(path)
	if err != nil || current != *expected {
		return errors.New("settings version changed")
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	raw["Groups"] = data
	data, err = json.MarshalIndent(raw, "", "  ")
	if err != nil || len(data) > 262144 {
		return errors.New("settings too large")
	}
	if err = atomicOperatorWrite(path, data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	*expected = hex.EncodeToString(sum[:])
	out.Version, out.Groups, out.Applied = *expected, groups, true
	return nil
}

// All actions remain behind the same-user/restricted-token checks in the local
// named pipe. No arbitrary JSON settings, file paths or commands are accepted.
func (m *operatorManager) handleExtended(ctx context.Context, req manageRequest, raw map[string]json.RawMessage, owner string, original []groupRecord, out *manageReply) bool {
	if isFileManagementAction(req.Action) {
		m.handleFileManagement(ctx, req, owner, original, out)
		return true
	}
	switch req.Action {
	case "list":
		out.Success = true
		return true
	case "tasks", "task-detail", "stop", "documents", "document-read", "document-delete-preview", "document-delete",
		"add", "enable", "disable", "group-add", "group-enable", "group-disable", "delete-preview", "delete", "cleanup-retry":
	default:
		return false
	}
	groups := make([]groupRecord, len(original))
	for i, group := range original {
		groups[i] = group
		groups[i].Members = append([]memberRecord(nil), group.Members...)
	}
	groupIndex := -1
	for i, group := range groups {
		if group.ID == req.Group {
			groupIndex = i
		}
	}
	admin := "wecom:" + owner
	if req.Action == "tasks" || req.Action == "task-detail" || req.Action == "stop" {
		return m.handleTaskRead(ctx, req, admin, groupIndex, out)
	}

	if strings.HasPrefix(req.Action, "document") {
		return m.handleDocumentRead(ctx, req, owner, admin, groups, groupIndex, out)
	}

	if req.Version == "" || req.Version != out.Version {
		out.Message = "配置版本已变化，请刷新后重新操作。"
		return true
	}
	expected := out.Version
	save := func() error { return saveManagedRoster(m.path, raw, owner, groups, &expected, out) }
	if req.Action == "delete-preview" || req.Action == "delete" {
		return m.handleMemberDelete(ctx, req, owner, admin, groups, save, out)
	}

	return m.handleRosterWrite(ctx, req, owner, groups, groupIndex, raw, out)
}

func (m *operatorManager) handleTaskRead(ctx context.Context, req manageRequest, admin string, groupIndex int, out *manageReply) bool {
	out.Groups = nil
	if m.service == nil {
		out.Message = "服务未运行。"
		return true
	}
	if req.Group != "" && groupIndex < 0 {
		out.Message = "群不存在。"
		return true
	}
	if req.Action == "stop" {
		if req.Group != "" {
			if _, err := m.service.AdminTasks(admin, req.Group, req.Task); err != nil {
				out.Message = "任务不存在。"
				return true
			}
		}
		response := m.service.AdminStop(ctx, admin, req.Task)
		out.Success, out.Tasks = response.ControlOK, response.Text
		return true
	}
	code := ""
	if req.Action == "task-detail" {
		if req.Task == "" {
			out.Message = "缺少任务编号。"
			return true
		}
		code = req.Task
	}
	items, err := m.service.AdminTasks(admin, req.Group, code)
	if err != nil {
		out.Message = "任务不可用。"
		return true
	}
	out.TaskItems, out.Success = items, true
	for _, task := range items {
		out.Tasks += fmt.Sprintf("%s | %s | %s | %s\n", task.ID, task.Actor.Group, strings.TrimPrefix(task.Actor.User, "wecom:"), task.State)
	}
	return true
}

func (m *operatorManager) handleDocumentRead(ctx context.Context, req manageRequest, owner, admin string, groups []groupRecord, groupIndex int, out *manageReply) bool {
	out.Groups = nil
	if m.service == nil || groupIndex < 0 {
		out.Message = "本地服务或群不可用。"
		return true
	}
	target := req.User
	if target == "" {
		target = owner
	}
	known := target == "public" || target == owner
	for _, user := range groups[groupIndex].Members {
		known = known || user.ID == target
	}
	if !known {
		out.Message = "成员不存在。"
		return true
	}
	if req.Action == "document-delete" && (req.Version == "" || req.Version != out.Version) {
		out.Message = "配置版本已变化，请刷新。"
		return true
	}
	if target != "public" {
		target = "wecom:" + target
	}
	result, err := m.service.AdminDocumentsContext(ctx, admin, req.Group, target, req.Action, req.Name, req.Confirm, req.FileVersion, req.Offset, req.Limit, out.Version)
	if err != nil {
		if errors.Is(err, groupassistant.ErrInventoryLimit) {
			fileManagementError(out, err)
			return true
		}
		out.Message = "文档操作未完成；文件可能已变化，请刷新核对。"
		return true
	}
	out.Documents, out.Success = &result, true
	return true
}

func (m *operatorManager) handleMemberDelete(ctx context.Context, req manageRequest, owner, admin string, groups []groupRecord, save func() error, out *manageReply) bool {
	if m.service == nil || req.User == owner || (req.Scope != "" && req.Scope != "all") {
		out.Message = "删除需要本地服务、有效成员和明确范围。"
		return true
	}
	var actors []groupassistant.Identity
	for _, group := range groups {
		if req.Scope != "all" && group.ID != req.Group {
			continue
		}
		for _, user := range group.Members {
			if user.ID == req.User {
				if user.Enabled || user.State == "disabling" || user.State == "cleanup_failed" {
					out.Message = "请先停用成员，并确认清理完成。"
					return true
				}
				actors = append(actors, groupassistant.Identity{Group: group.ID, User: "wecom:" + user.ID})
			}
		}
	}
	if len(actors) == 0 {
		out.Message = "成员不存在。"
		return true
	}
	if req.Action == "delete-preview" {
		preview, err := m.service.PreviewMemberDelete(ctx, admin, actors)
		if err != nil {
			if errors.Is(err, groupassistant.ErrBusy) {
				out.Message = "临时工作区仍有任务或残留数据；请等待任务结束或核对清理后重试。"
			} else {
				out.Message = "无法确认成员数据或任务清理状态。"
			}
			return true
		}
		out.DeletePreview, out.Success = &preview, true
		return true
	}
	matches := func(group, user string) bool {
		for _, id := range actors {
			if id.Group == group && id.User == "wecom:"+user {
				return true
			}
		}
		return false
	}
	stage := func(state string) error {
		for i := range groups {
			for j := range groups[i].Members {
				if matches(groups[i].ID, groups[i].Members[j].ID) {
					groups[i].Members[j].State = state
				}
			}
		}
		return save()
	}
	finish := func() error {
		for i := range groups {
			var keep []memberRecord
			for _, user := range groups[i].Members {
				if !matches(groups[i].ID, user.ID) {
					keep = append(keep, user)
				}
			}
			groups[i].Members = keep
		}
		return save()
	}
	if err := m.service.DeleteMembers(ctx, admin, req.Confirm, actors, stage, finish); err != nil {
		out.Message = "删除未完成或确认已失效；成员保持停用，请刷新后重新预览。"
		out.CleanupReady = false
		return true
	}
	out.Success, out.Message = true, "私人数据及成员记录已删除，公共资料保留。"
	return true
}

func (m *operatorManager) handleCleanupRetry(ctx context.Context, req manageRequest, groups []groupRecord, groupIndex int, save func() error, out *manageReply) bool {
	if m.service == nil {
		out.Message = "请在重启后的本地服务中重新核验清理。"
		return true
	}
	for _, user := range groups[groupIndex].Members {
		if user.ID == req.User && (user.State == "deleting" || user.State == "delete_failed") {
			out.Message = "请重新预览并完成删除，不能用清理恢复绕过未完成的删除。"
			return true
		}
	}
	if err := m.service.RetryMemberCleanup(ctx, req.Group, req.User); err != nil {
		out.Message = "清理仍未确认，不能恢复启用或删除。"
		return true
	}
	if req.User == "" {
		if groups[groupIndex].Enabled {
			out.Message = "请先停用群。"
			return true
		}
		groups[groupIndex].State = "disabled"
	} else {
		found := false
		for j := range groups[groupIndex].Members {
			u := &groups[groupIndex].Members[j]
			if u.ID == req.User && !u.Enabled {
				u.State = "disabled"
				found = true
			}
		}
		if !found {
			out.Message = "请先停用成员。"
			return true
		}
	}
	if err := save(); err != nil {
		out.Message = "清理状态保存失败。"
		return true
	}
	out.Success = true
	return true
}

func (m *operatorManager) handleRosterWrite(ctx context.Context, req manageRequest, owner string, groups []groupRecord, groupIndex int, raw map[string]json.RawMessage, out *manageReply) bool {
	expected := out.Version
	save := func() error { return saveManagedRoster(m.path, raw, owner, groups, &expected, out) }
	if req.Action == "group-add" {
		if groupIndex >= 0 || req.Group == "" || req.Group != strings.TrimSpace(req.Group) || len(req.Group) > 250 || strings.IndexFunc(req.Group, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 || strings.HasPrefix(req.Group, "YOUR_") || strings.HasPrefix(req.Group, "wecom:") {
			out.Message = "群 ID 无效或已存在。"
			return true
		}
		groups = append(groups, groupRecord{ID: req.Group, Enabled: true, State: "enabled"})
		groupIndex = len(groups) - 1
	} else if groupIndex < 0 {
		out.Message = "群不存在。"
		return true
	}
	if req.Action == "cleanup-retry" {
		return m.handleCleanupRetry(ctx, req, groups, groupIndex, save, out)
	}

	if message := editRosterEntry(req, owner, &groups[groupIndex]); message != "" {
		out.Message = message
		return true
	}
	ids, members := activeRoster(groups)
	var err error
	if m.service != nil {
		err = m.service.ApplyRoster(ctx, ids, members, save)
	} else {
		err = save()
	}
	if err != nil && !out.Applied {
		out.Message = "未应用：保存、版本或任务清理检查失败。"
		return true
	}
	if req.Action == "disable" || req.Action == "group-disable" {
		state := "disabled"
		if err != nil {
			state = "cleanup_failed"
		}
		if req.Action == "group-disable" {
			groups[groupIndex].State = state
		} else {
			for j := range groups[groupIndex].Members {
				if groups[groupIndex].Members[j].ID == req.User {
					groups[groupIndex].Members[j].State = state
				}
			}
		}
		if saveErr := save(); saveErr != nil {
			err = errors.Join(err, saveErr)
		}
	}
	out.Success = true
	out.CleanupReady = err == nil
	if err != nil {
		out.Message = "权限已撤销，但清理或状态保存未确认；不可恢复启用或删除。"
	} else if m.service == nil {
		out.Message = "已保存，下次启动生效。"
	} else {
		out.Message = "已保存并实时生效。"
	}
	return true
}

func editRosterEntry(req manageRequest, owner string, group *groupRecord) string {
	if req.Action == "group-enable" || req.Action == "group-disable" {
		g := group
		if g.State == "disabling" || g.State == "cleanup_failed" {
			return "请先重新核验群任务清理。"
		}
		g.Enabled = req.Action == "group-enable"
		if g.Enabled {
			g.State = "enabled"
		} else {
			g.State = "disabling"
		}
	} else if req.Action != "group-add" {
		if req.User == owner || groupassistant.ValidateMembers(owner, []string{req.Group}, map[string][]string{req.Group: {req.User}}) != nil {
			return "成员 ID 无效或与管理员相同。"
		}
		index := -1
		for j, u := range group.Members {
			if u.ID == req.User {
				index = j
			}
		}
		if req.Action == "add" {
			if index >= 0 {
				return "成员已登记。"
			}
			group.Members = append(group.Members, memberRecord{ID: req.User, Enabled: true, State: "enabled"})
		} else {
			if index < 0 {
				return "成员不存在。"
			}
			u := &group.Members[index]
			if req.Action == "disable" && u.State != "" && u.State != "disabled" && u.State != "enabled" {
				return "请先完成清理恢复或重新预览删除，不能覆盖未完成状态。"
			}
			if req.Action == "enable" && u.State != "" && u.State != "disabled" && u.State != "enabled" {
				return "请先完成清理或删除恢复。"
			}
			u.Enabled = req.Action == "enable"
			if u.Enabled {
				u.State = "enabled"
			} else {
				u.State = "disabling"
			}
		}
	}
	return ""
}
