package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenhg5/cc-connect/groupassistant"
)

func checkNativeCommand(ctx context.Context, command, directory string) (err error) {
	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}
	root, err := os.MkdirTemp(directory, ".native-check-")
	if err != nil {
		return err
	}
	defer func() {
		if filepath.Dir(root) != directory {
			err = errors.Join(err, fmt.Errorf("invalid check cleanup path"))
			return
		}
		err = errors.Join(err, os.RemoveAll(root))
	}()
	work, home := filepath.Join(root, "work"), filepath.Join(root, "home")
	for _, dir := range []string{home, filepath.Join(work, "mine"), filepath.Join(work, "public"), filepath.Join(work, ".ccga-tmp")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	runner := &groupassistant.NativeRunner{Command: command, Home: home, WindowsSandbox: "mxc", Verified: true, AutomaticRoutine: true}
	_, err = runner.Inspect(ctx, &groupassistant.Snapshot{Dir: work, Identity: groupassistant.Identity{Group: "native-check", User: "wecom:probe"}}, "handshake")
	return err
}
