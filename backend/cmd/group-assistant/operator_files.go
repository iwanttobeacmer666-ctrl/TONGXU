package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/groupassistant"
)

func isFileManagementAction(action string) bool {
	switch action {
	case "documents-delete-preview", "documents-delete", "documents-clear-preview", "documents-clear",
		"temporary-files", "temporary-clear-preview", "temporary-clear", "file-operation":
		return true
	}
	return false
}

func fileManagementError(out *manageReply, err error) {
	switch {
	case errors.Is(err, groupassistant.ErrInventoryLimit):
		out.Code, out.Message = "inventory_limit", "当前文件区超过 100000 个文件或目录，无法完整列出或预览；本次未执行删除。"
	case errors.Is(err, groupassistant.ErrChanged):
		out.Code, out.Message = "changed", "文件或目录已变化，本次确认不能继续；请刷新并重新预览。"
	case errors.Is(err, groupassistant.ErrBusy):
		out.Code, out.Message = "busy", "任务或内部操作尚未结束，或预览数量已达上限；请等待后重试，不会自动停止任务。"
	case errors.Is(err, groupassistant.ErrDenied):
		out.Code, out.Message = "confirmation_invalid", "权限、操作范围或确认无效；请完整查看预览，过期后重新预览。"
	case errors.Is(err, groupassistant.ErrInvalid):
		out.Code, out.Message = "invalid_request", "请求范围、文件选择或分页参数无效。"
	default:
		out.Code, out.Message = "unavailable", "操作未完成；请查看逐项结果并刷新核对，不能视为清理成功。"
	}
}

func documentTarget(owner string, groups []groupRecord, group, user string) (string, bool) {
	if user == "" {
		user = owner
	}
	for _, g := range groups {
		if g.ID != group {
			continue
		}
		if user == "public" {
			return user, true
		}
		if user == owner {
			return "wecom:" + user, true
		}
		for _, member := range g.Members {
			if member.ID == user {
				return "wecom:" + user, true
			}
		}
	}
	return "", false
}

func (m *operatorManager) handleFileManagement(ctx context.Context, req manageRequest, owner string, groups []groupRecord, out *manageReply) {
	out.Groups, out.CleanupReady = nil, false
	if m.service == nil {
		out.Code, out.Message = "offline", "此文件管理操作需要本地服务在线。"
		return
	}
	if req.Action != "temporary-files" && (req.Version == "" || req.Action != "file-operation" && req.Version != out.Version) {
		out.Code, out.Message = "version_conflict", "配置版本已变化，请刷新核对后重新预览，不会自动覆盖。"
		return
	}
	if req.Name != "" || req.Task != "" || req.FileVersion != "" || req.Action != "file-operation" && req.Scope != "" {
		fileManagementError(out, groupassistant.ErrInvalid)
		return
	}
	kind := ""
	switch req.Action {
	case "documents-delete-preview", "documents-delete":
		kind = groupassistant.FileDeleteSelected
	case "documents-clear-preview", "documents-clear":
		kind = groupassistant.FileClearDocuments
	case "temporary-files", "temporary-clear-preview", "temporary-clear":
		kind = groupassistant.FileClearTemporary
	case "file-operation":
		kind = req.Scope
	}
	if kind != groupassistant.FileDeleteSelected && kind != groupassistant.FileClearDocuments && kind != groupassistant.FileClearTemporary {
		fileManagementError(out, groupassistant.ErrInvalid)
		return
	}
	target := ""
	if kind == groupassistant.FileClearTemporary {
		if req.Group != "" || req.User != "" || len(req.Names) != 0 {
			fileManagementError(out, groupassistant.ErrInvalid)
			return
		}
	} else {
		var ok bool
		target, ok = documentTarget(owner, groups, req.Group, req.User)
		if !ok {
			out.Code, out.Message = "invalid_scope", "群或成员不存在，请刷新核对。"
			return
		}
	}
	if len(req.Names) != 0 && req.Action != "documents-delete-preview" {
		fileManagementError(out, groupassistant.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	admin := "wecom:" + owner
	var view groupassistant.FileOperationView
	var err error
	switch req.Action {
	case "temporary-files":
		view, err = m.service.TemporaryFiles(ctx, admin, req.Offset, req.Limit)
	case "documents-delete-preview", "documents-clear-preview", "temporary-clear-preview":
		if req.Confirm != "" || req.Offset != 0 {
			fileManagementError(out, groupassistant.ErrInvalid)
			return
		}
		view, err = m.service.PreviewFileOperation(ctx, admin, req.Group, target, kind, req.Version, req.Names, req.Limit)
	case "file-operation":
		view, err = m.service.FileOperationPage(admin, req.Group, target, kind, req.Version, req.Confirm, req.Offset, req.Limit)
	default:
		view, err = m.service.ConfirmFileOperation(ctx, admin, req.Group, target, kind, req.Version, req.Confirm)
	}
	if view.Kind != "" {
		if view.User != "public" {
			view.User = strings.TrimPrefix(view.User, "wecom:")
		}
		out.FileOperation = &view
	}
	if err != nil {
		fileManagementError(out, err)
		return
	}
	out.Success = true
	if req.Action == "documents-delete" || req.Action == "documents-clear" || req.Action == "temporary-clear" {
		out.Success, out.CleanupReady = view.Complete, view.Complete
		out.Message = "清理已完成；成员、会话及其他文件区保留。"
	}
}
