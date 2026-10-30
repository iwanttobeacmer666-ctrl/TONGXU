//go:build !windows

package main

import (
	"context"
	"errors"
)

func startOperatorPipe(context.Context, string, *operatorManager) (func(), error) {
	return nil, errors.New("local management requires Windows")
}
func operatorRemote(context.Context, string, manageRequest) (manageReply, error) {
	return manageReply{}, errors.New("local management requires Windows")
}
func replaceOperator(string, string) error { return errors.New("local management requires Windows") }
func operatorLock(string) (func(), error) {
	return nil, errors.New("local management requires Windows")
}
func operatorOfflineSafe() error  { return errors.New("local management requires Windows") }
func operatorLocalTrusted() error { return errors.New("local management requires Windows") }
