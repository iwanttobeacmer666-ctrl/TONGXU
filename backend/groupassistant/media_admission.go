package groupassistant

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/chenhg5/cc-connect/core"
)

var errMediaBusy = errors.New("media downloader busy")

type mediaReservationKey struct{}
type mediaReservation struct {
	service *Service
	message *core.Message
	task    *groupTask
	claimed atomic.Bool
}

// AdmitMedia uses the same task records as ordinary work, so member revocation,
// stop, queue capacity and the cross-group per-user limit also cover downloads.
func (s *Service) AdmitMedia(ctx context.Context, msg *core.Message, count int) (*core.InboundMediaAdmission, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if s.updatePaused {
		return nil, ErrBusy
	}
	id, err := s.identity(msg)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if count < 1 || count > 20 || len(msg.Content)+len(msg.ExtraContent) > 32<<10 {
		return nil, ErrInvalid
	}
	command, _, _ := strings.Cut(strings.TrimSpace(msg.Content), " ")
	if strings.HasPrefix(command, "/") {
		if command == "/new" || command == "/compact" {
			return nil, ErrInvalid
		}
		// Short commands do not consume attachments or a task slot.
		return nil, nil
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	task, taskCtx, err := s.addTask(ctx, id)
	if err != nil {
		<-s.slots
		return nil, err
	}
	select {
	case s.mediaDownloads <- struct{}{}:
	default:
		s.finishTask(task)
		<-s.slots
		return nil, errMediaBusy
	}
	task.mu.Lock()
	task.state = "downloading"
	task.mu.Unlock()
	reservation := &mediaReservation{service: s, message: msg, task: task}
	var downloadOnce, closeOnce sync.Once
	downloaded := func() {
		downloadOnce.Do(func() {
			<-s.mediaDownloads
			task.mu.Lock()
			if !task.stopping {
				task.state = "queued"
			}
			task.mu.Unlock()
		})
	}
	return &core.InboundMediaAdmission{
		Context:  context.WithValue(taskCtx, mediaReservationKey{}, reservation),
		MaxBytes: MaxFileBytes, Downloaded: downloaded,
		Close: func() { closeOnce.Do(func() { downloaded(); s.finishTask(task); <-s.slots }) },
	}, nil
}

func (s *Service) MediaFailure(err error) string {
	if errors.Is(err, errMediaBusy) {
		return s.T(core.MsgGroupMediaBusy)
	}
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrBusy) || errors.Is(err, errMemberBusy) {
		return s.failure(err).Text
	}
	return s.T(core.MsgGroupMediaFailed)
}
