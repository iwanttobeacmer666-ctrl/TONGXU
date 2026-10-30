package groupassistant

import (
	"context"
	"errors"
	"os"

	"github.com/chenhg5/cc-connect/core"
)

type ThreadCleaner interface {
	DeleteThreads(context.Context, string, []string) error
}

func (n *NativeRunner) DeleteThreads(ctx context.Context, workdir string, ids []string) (err error) {
	if len(ids) == 0 {
		return nil
	}
	if err = n.Ready(); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(workdir, ".ccga-thread-delete-")
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			n.unhealthy.Store(true)
			err = errors.Join(err, removeErr)
		}
	}()
	opts, err := n.agentOptions(&Snapshot{Identity: Identity{Group: "local-control", User: n.Admin}, Dir: dir})
	if err != nil {
		return err
	}
	opts["native_control_only"] = true
	agent, err := core.CreateAgent("codex", opts)
	if err != nil {
		return err
	}
	defer agent.Stop()
	session, err := startNativeSession(ctx, agent, "")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			n.unhealthy.Store(true)
			err = errors.Join(err, ErrUnavailable)
		}
	}()
	cleaner, ok := session.(interface {
		DeleteNativeThreads(context.Context, []string) error
	})
	if !ok {
		return ErrUnavailable
	}
	return cleaner.DeleteNativeThreads(ctx, ids)
}
