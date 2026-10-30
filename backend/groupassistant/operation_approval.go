package groupassistant

import (
	"context"
	"sync"
	"time"
)

type operationApproval struct {
	actor    Identity
	expires  time.Time
	context  context.Context
	decision chan bool
}

// OperationApprovals binds each native execution decision to the authenticated
// group owner. Ordinary file confirmations remain bound to their initiating user.
type OperationApprovals struct {
	mu      sync.Mutex
	owner   string
	pending map[string]*operationApproval
	now     func() time.Time
	ttl     time.Duration
}

func NewOperationApprovals(owner string) *OperationApprovals {
	return &OperationApprovals{owner: owner, pending: map[string]*operationApproval{}, now: time.Now, ttl: 5 * time.Minute}
}

func (a *OperationApprovals) Wait(ctx context.Context, actor Identity, notify func(string) error) (bool, error) {
	if !validIdentity(actor) || a.owner == "" || notify == nil {
		return false, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	code, err := token()
	if err != nil {
		return false, err
	}
	request := &operationApproval{actor: actor, expires: a.now().Add(a.ttl), context: ctx, decision: make(chan bool, 1)}
	a.mu.Lock()
	if len(a.pending) >= 10 {
		a.mu.Unlock()
		return false, ErrBusy
	}
	a.pending[code] = request
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, code)
		a.mu.Unlock()
	}()
	if err := notify(code); err != nil {
		return false, err
	}
	timer := time.NewTimer(a.ttl)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return false, context.DeadlineExceeded
	case approved := <-request.decision:
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return approved, nil
	}
}

func (a *OperationApprovals) Resolve(owner Identity, code string, approve bool) error {
	if !validIdentity(owner) || owner.User != a.owner {
		return ErrDenied
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	request, ok := a.pending[code]
	if !ok || request.actor.Group != owner.Group {
		return ErrDenied
	}
	if request.context.Err() != nil || !a.now().Before(request.expires) {
		delete(a.pending, code)
		return ErrChanged
	}
	delete(a.pending, code)
	request.decision <- approve
	return nil
}
