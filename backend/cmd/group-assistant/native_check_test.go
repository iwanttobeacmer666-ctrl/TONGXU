package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNativeCheckRejectsWrongExecutableAndCleansTemporaryHome(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := checkNativeCommand(ctx, command, dir); err == nil {
		t.Fatal("test runner accepted as native Codex")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("native check left temporary state", entries, err)
	}
}

func TestInstalledIsolatedNativeHandshake(t *testing.T) {
	command := os.Getenv("CC_GROUP_MXC_TEST_COMMAND")
	if command == "" {
		t.Skip("explicit installed CLI handshake check")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := checkNativeCommand(ctx, command, dir); err != nil {
		t.Fatal("isolated native handshake failed", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("native check left state", entries, err)
	}
}
