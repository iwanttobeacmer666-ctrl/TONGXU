//go:build windows

package codex

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The full native Codex toolchain stays enabled. The job only confines process
// lifetime, so cancelling a bounded task cannot leave background descendants.
func startBoundedProcess(cmd *exec.Cmd) (func() error, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	var mu sync.Mutex
	closed := false
	attached := false
	cleanup := func() error {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		stopErr := windows.TerminateJobObject(job, 1)
		waitErr := waitForJobEmpty(job)
		return errors.Join(stopErr, waitErr, windows.CloseHandle(job))
	}
	cmd.Cancel = func() error {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil
		}
		if attached {
			return windows.TerminateJobObject(job, 1)
		}
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	cmd.SysProcAttr.HideWindow = true
	if err = cmd.Start(); err != nil {
		cleanup()
		return nil, err
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, p)
		windows.CloseHandle(p)
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		cleanup()
		return nil, fmt.Errorf("attach native process boundary: %w", err)
	}
	mu.Lock()
	attached = true
	mu.Unlock()
	if err := resumeSuspendedProcess(uint32(cmd.Process.Pid)); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

// Create suspended -> assign job -> resume: startup children cannot precede
// job assignment. Only the new process's initial thread is resumed.
func resumeSuspendedProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if e != nil {
			return e
		}
		previous, e := windows.ResumeThread(thread)
		windows.CloseHandle(thread)
		if e != nil {
			return e
		}
		if previous != 1 {
			return fmt.Errorf("unexpected initial thread suspend count: %d", previous)
		}
		return nil
	}
	return fmt.Errorf("initial suspended thread not found: %w", err)
}

type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}

func waitForJobEmpty(job windows.Handle) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var accounting jobAccounting
		if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return err
		}
		if accounting.ActiveProcesses == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bounded task descendants have not exited")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
