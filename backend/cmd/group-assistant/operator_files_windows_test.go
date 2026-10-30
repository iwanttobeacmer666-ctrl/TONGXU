package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/groupassistant"
)

func TestLocalFileManagementRoutesScopeVersionAndOfflineGuards(t *testing.T) {
	m, store := extendedManager(t)
	ctx := context.Background()
	id := groupassistant.Identity{Group: "group", User: "wecom:alice"}
	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := store.Put(id, groupassistant.FileRef{Name: name}, []byte("private")); err != nil {
			t.Fatal(err)
		}
	}
	view := m.handle(ctx, manageRequest{Action: "list"})
	preview := m.handle(ctx, manageRequest{Action: "documents-delete-preview", Version: view.Version, Group: "group", User: "alice", Names: []string{"one.txt"}})
	if !preview.Success || preview.FileOperation == nil || preview.FileOperation.User != "alice" || !preview.FileOperation.ReviewComplete || preview.CleanupReady || preview.Applied || len(preview.Groups) != 0 {
		t.Fatal(preview)
	}
	for _, req := range []manageRequest{
		{Action: "documents-delete", Version: "old", Group: "group", User: "alice", Confirm: preview.FileOperation.Token},
		{Action: "documents-delete", Version: view.Version, Group: "group", User: "public", Confirm: preview.FileOperation.Token},
		{Action: "documents-clear", Version: view.Version, Group: "group", User: "alice", Confirm: preview.FileOperation.Token},
		{Action: "documents-clear-preview", Version: view.Version, Group: "group", User: "unknown"},
		{Action: "temporary-clear-preview", Version: view.Version, Group: "group"},
	} {
		if got := m.handle(ctx, req); got.Success || got.Code == "" {
			t.Fatal("invalid route accepted", req, got)
		}
	}
	deleted := m.handle(ctx, manageRequest{Action: "documents-delete", Version: preview.FileOperation.Version, Group: preview.FileOperation.Group, User: preview.FileOperation.User, Confirm: preview.FileOperation.Token})
	if !deleted.Success || !deleted.CleanupReady || !deleted.FileOperation.Complete || deleted.Applied {
		t.Fatal(deleted)
	}
	if _, err := store.Read(id, groupassistant.FileRef{Name: "two.txt"}); err != nil {
		t.Fatal(err)
	}
	// A current Version cannot rebind a token staged against an earlier config.
	preview = m.handle(ctx, manageRequest{Action: "documents-clear-preview", Version: view.Version, Group: "group", User: "alice"})
	changed := m.handle(ctx, manageRequest{Action: "add", Version: view.Version, Group: "group", User: "bob"})
	if !changed.Success {
		t.Fatal(changed)
	}
	if got := m.handle(ctx, manageRequest{Action: "documents-clear", Version: changed.Version, Group: "group", User: "alice", Confirm: preview.FileOperation.Token}); got.Success {
		t.Fatal("preview rebound to current configuration", got)
	}
	if got := m.handle(ctx, manageRequest{Action: "temporary-files", Limit: 10}); !got.Success || got.FileOperation.Kind != "temporary" {
		t.Fatal(got)
	}
	offline := &operatorManager{path: m.path}
	for _, action := range []string{"documents-delete-preview", "documents-clear", "temporary-files", "temporary-clear-preview", "temporary-clear", "file-operation"} {
		if got := offline.handle(ctx, manageRequest{Action: action, Version: changed.Version}); got.Success || got.Code != "offline" || got.Applied || got.CleanupReady {
			t.Fatal("offline file operation accepted", action, got)
		}
	}
}

func TestLocalFileManagementPagedPreviewAndClear(t *testing.T) {
	m, store := extendedManager(t)
	ctx := context.Background()
	id := groupassistant.Identity{Group: "group", User: "wecom:owner"}
	for _, name := range []string{"a", "sub/b", "c"} {
		if _, err := store.Put(id, groupassistant.FileRef{Owner: "public", Name: name}, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	version := m.handle(ctx, manageRequest{Action: "list"}).Version
	p := m.handle(ctx, manageRequest{Action: "documents-clear-preview", Group: "group", User: "public", Version: version, Limit: 1})
	if !p.Success || p.FileOperation.Files != 3 || p.FileOperation.ReviewComplete {
		t.Fatal(p)
	}
	confirmation := manageRequest{Action: "documents-clear", Group: "group", User: "public", Version: version, Confirm: p.FileOperation.Token}
	if got := m.handle(ctx, confirmation); got.Success {
		t.Fatal("unreviewed clear accepted", got)
	}
	for !p.FileOperation.ReviewComplete {
		p = m.handle(ctx, manageRequest{Action: "file-operation", Scope: "documents", Group: "group", User: "public", Version: version, Confirm: p.FileOperation.Token, Offset: p.FileOperation.NextOffset, Limit: 1})
		if !p.Success {
			t.Fatal(p)
		}
	}
	result := m.handle(ctx, confirmation)
	if !result.Success || !result.FileOperation.Complete || result.FileOperation.Deleted != 4 {
		t.Fatal(result)
	}
	page := m.handle(ctx, manageRequest{Action: "documents", Group: "group", User: "public"})
	if !page.Success || page.Documents.Total != 0 {
		t.Fatal(page)
	}
}

func TestOperatorFilePipeHandlesBoundedLargerSelectionsWithoutLeakingSettings(t *testing.T) {
	m, store := extendedManager(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	closePipe, err := startOperatorPipe(ctx, m.path, m)
	if err != nil {
		t.Fatal(err)
	}
	defer closePipe()
	var names []string
	for i := 0; i < 30; i++ {
		name := strings.Repeat("long-name-", 15) + fmt.Sprintf("%02d.txt", i)
		names = append(names, name)
		if _, err := store.Put(groupassistant.Identity{Group: "group", User: "wecom:alice"}, groupassistant.FileRef{Name: name}, []byte("private")); err != nil {
			t.Fatal(err)
		}
	}
	version := m.handle(ctx, manageRequest{Action: "list"}).Version
	req := manageRequest{Action: "documents-delete-preview", Version: version, Group: "group", User: "alice", Names: names}
	encoded, _ := json.Marshal(req)
	if len(encoded) <= 4096 {
		t.Fatal("fixture did not exceed old request size")
	}
	if _, err := decodeManage(encoded); err != nil {
		t.Fatal(err)
	}
	result, err := operatorRemote(ctx, m.path, req)
	if err != nil || !result.Success || result.FileOperation.Files != 30 {
		t.Fatal(result, err)
	}
	encoded, err = json.Marshal(result)
	if err != nil || len(encoded) > 262144 || strings.Contains(string(encoded), "PRIVATE-FIXTURE") || strings.Contains(string(encoded), "KEEP-PATH") {
		t.Fatal("unbounded/private pipe response", err)
	}
	if _, err := decodeManage([]byte(strings.Repeat(" ", maxManageRequest+1))); err == nil {
		t.Fatal("oversized request accepted")
	}
	if _, err := decodeManage([]byte(`{"Action":"temporary-clear","Path":"D:/"}`)); err == nil {
		t.Fatal("arbitrary host path accepted")
	}
}

func TestLocalFileSingleDeleteCannotBypassConfigurationThroughChatConfirmation(t *testing.T) {
	m, store := extendedManager(t)
	ctx := context.Background()
	id := groupassistant.Identity{Group: "group", User: "wecom:alice"}
	if _, err := store.Put(id, groupassistant.FileRef{Name: "keep.txt"}, []byte("private")); err != nil {
		t.Fatal(err)
	}
	version := m.handle(ctx, manageRequest{Action: "list"}).Version
	p := m.handle(ctx, manageRequest{Action: "document-delete-preview", Group: "group", User: "alice", Name: "keep.txt"})
	if !p.Success {
		t.Fatal(p)
	}
	if err := store.Confirm(groupassistant.Identity{Group: "group", User: "wecom:owner"}, p.Documents.Pending.Token); err == nil {
		t.Fatal("local token confirmed through unversioned chat path")
	}
	changed := m.handle(ctx, manageRequest{Action: "add", Version: version, Group: "group", User: "bob"})
	if !changed.Success {
		t.Fatal(changed)
	}
	if got := m.handle(ctx, manageRequest{Action: "document-delete", Group: "group", User: "alice", Version: changed.Version, Confirm: p.Documents.Pending.Token}); got.Success {
		t.Fatal("single token rebound after config change", got)
	}
	if _, err := store.Read(id, groupassistant.FileRef{Name: "keep.txt"}); err != nil {
		t.Fatal(err)
	}
}
