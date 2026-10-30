//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func pipeName(path string) string {
	p, _ := filepath.Abs(path)
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(p))))
	return `\\.\pipe\cc-group-manager-` + hex.EncodeToString(sum[:16])
}

func trustedProcess(pid uint32, checkImage bool) error {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	var token windows.Token
	if err = windows.OpenProcessToken(p, windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer token.Close()
	restricted, err := token.IsRestricted()
	if err != nil || restricted {
		return errors.New("restricted client")
	}
	var container uint32
	var length uint32
	// TOKEN_INFORMATION_CLASS TokenIsAppContainer=29, verified against the
	// existing Windows headers; x/sys's older enum stops at TokenLogonSid.
	if err = windows.GetTokenInformation(token, 29, (*byte)(unsafe.Pointer(&container)), 4, &length); err != nil || container != 0 {
		return errors.New("container client")
	}
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	own, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !user.User.Sid.Equals(own.User.Sid) {
		return errors.New("different Windows user")
	}
	// Do not allow low-integrity clients, even if they share the user's SID.
	buf := make([]byte, 256)
	if err = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buf[0], uint32(len(buf)), &length); err != nil {
		return err
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	sid := label.Label.Sid
	if sid.SubAuthority(uint32(sid.SubAuthorityCount()-1)) < 0x2000 {
		return errors.New("low integrity client")
	}
	if checkImage {
		name := make([]uint16, 32768)
		n := uint32(len(name))
		if err = windows.QueryFullProcessImageName(p, 0, &name[0], &n); err != nil {
			return err
		}
		current, e := os.Executable()
		if e != nil || !strings.EqualFold(filepath.Clean(current), filepath.Clean(windows.UTF16ToString(name[:n]))) {
			return errors.New("unexpected broker executable")
		}
	}
	return nil
}

// All pipe I/O is overlapped and bounded; cancellation drains completion before
// releasing the OVERLAPPED/event buffers. No TCP listener or OS configuration.
func pipeIO(ctx context.Context, h windows.Handle, connect, write bool, data []byte) (int, error) {
	// The kernel owns this buffer until overlapped completion is drained.
	defer runtime.KeepAlive(data)
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	ov := windows.Overlapped{HEvent: event}
	var n uint32
	// The kernel retains these addresses after the syscall returns. KeepAlive
	// alone cannot prevent Go stack growth from moving an OVERLAPPED/buffer.
	var pinned runtime.Pinner
	pinned.Pin(&ov)
	pinned.Pin(&n)
	if len(data) != 0 {
		pinned.Pin(&data[0])
	}
	defer pinned.Unpin() // every pending path below drains completion first
	if connect {
		err = windows.ConnectNamedPipe(h, &ov)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return 0, nil
		}
	} else if write {
		err = windows.WriteFile(h, data, &n, &ov)
	} else {
		err = windows.ReadFile(h, data, &n, &ov)
	}
	if err == nil {
		return int(n), nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, err
	}
	for {
		result, e := windows.WaitForSingleObject(event, 100)
		if e != nil {
			err = e
			break
		}
		if result == windows.WAIT_OBJECT_0 {
			returnResult := windows.GetOverlappedResult(h, &ov, &n, false)
			return int(n), returnResult
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
	}
	_ = windows.CancelIoEx(h, &ov)
	_ = windows.GetOverlappedResult(h, &ov, &n, true)
	return 0, err
}

func startOperatorPipe(ctx context.Context, path string, manager *operatorManager) (func(), error) {
	if err := trustedProcess(windows.GetCurrentProcessId(), false); err != nil {
		return nil, err
	}
	own, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + own.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	name, err := windows.UTF16PtrFromString(pipeName(path))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_MESSAGE|windows.PIPE_READMODE_MESSAGE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 262144, maxManageRequest, 0, &sa)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer windows.CloseHandle(h)
		for ctx.Err() == nil {
			if _, err := pipeIO(ctx, h, true, false, nil); err != nil {
				return
			}
			func() {
				defer windows.DisconnectNamedPipe(h)
				var pid uint32
				if windows.GetNamedPipeClientProcessId(h, &pid) != nil || trustedProcess(pid, false) != nil {
					return
				}
				readCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				buf := make([]byte, maxManageRequest+1)
				n, err := pipeIO(readCtx, h, false, false, buf)
				stop()
				if err != nil {
					return
				}
				req, err := decodeManage(buf[:n])
				if err != nil {
					return
				}
				reply := manager.handle(ctx, req)
				// Stop through the existing main lifecycle only after the reply/ACK
				// attempt. Request acceptance is not a cleanup completion claim.
				if req.Action == "service-stop" && reply.Success && manager.shutdown != nil {
					defer manager.shutdown()
				}
				encoded, err := json.Marshal(reply)
				if err != nil || len(encoded) > 262144 {
					return
				}
				ioCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				defer stop()
				if _, err = pipeIO(ioCtx, h, false, true, encoded); err != nil {
					return
				}
				// Client ACK prevents DisconnectNamedPipe discarding an unread response.
				_, _ = pipeIO(ioCtx, h, false, false, make([]byte, 8))
			}()
		}
	}()
	return func() { cancel(); <-done }, nil
}

func operatorRemote(ctx context.Context, path string, req manageRequest) (manageReply, error) {
	var out manageReply
	if err := trustedProcess(windows.GetCurrentProcessId(), false); err != nil {
		return out, err
	}
	name, err := windows.UTF16PtrFromString(pipeName(path))
	if err != nil {
		return out, err
	}
	var h windows.Handle
	deadline := time.Now().Add(2 * time.Second)
	seenBusy := false
	for {
		h, err = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, nilHandle)
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			break
		}
		seenBusy = true
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !seenBusy {
			return out, errNoOperatorServer
		}
		return out, err
	}
	defer windows.CloseHandle(h)
	var pid uint32
	if err = windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return out, err
	}
	if err = trustedProcess(pid, true); err != nil {
		return out, err
	}
	mode := uint32(windows.PIPE_READMODE_MESSAGE)
	if err = windows.SetNamedPipeHandleState(h, &mode, nil, nil); err != nil {
		return out, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	if len(data) > maxManageRequest {
		return out, errors.New("oversized local management request")
	}
	if _, err = pipeIO(ctx, h, false, true, data); err != nil {
		return out, err
	}
	buf := make([]byte, 262145)
	n, err := pipeIO(ctx, h, false, false, buf)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(buf[:n], &out); err != nil {
		return out, err
	}
	_, err = pipeIO(ctx, h, false, true, []byte("ACK"))
	return out, err
}

const nilHandle windows.Handle = 0

func operatorLocalTrusted() error { return trustedProcess(windows.GetCurrentProcessId(), false) }

func replaceOperator(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
func operatorLock(path string) (func(), error) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	return func() { windows.CloseHandle(h) }, nil
}

func operatorOfflineSafe() error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	current, err := os.Executable()
	if err != nil {
		return err
	}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == windows.GetCurrentProcessId() || !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(current)) {
			continue
		}
		p, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e != nil {
			return errors.New("other instance cannot be inspected")
		}
		name := make([]uint16, 32768)
		n := uint32(len(name))
		e = windows.QueryFullProcessImageName(p, 0, &name[0], &n)
		windows.CloseHandle(p)
		if e != nil || strings.EqualFold(filepath.Clean(current), filepath.Clean(windows.UTF16ToString(name[:n]))) {
			return errors.New("existing instance has no available management endpoint")
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return nil
}
