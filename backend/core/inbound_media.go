package core

import "context"

// InboundMediaAdmission reserves resources before a platform downloads media.
// Downloaded releases download capacity; Close releases the task reservation
// after delivery finishes (or any download fails). Both must be idempotent.
type InboundMediaAdmission struct {
	Context    context.Context
	MaxBytes   int64
	Downloaded func()
	Close      func()
}

// A nil admission without an error means deliver the text without downloading
// attachments, for example an identity query from an unenrolled user.
type InboundMediaGuard interface {
	AdmitMedia(context.Context, *Message, int) (*InboundMediaAdmission, error)
	MediaFailure(error) string
}

// Configure before Start. The guarded handler receives the reserved context;
// platforms without this optional capability cannot enforce pre-download policy.
type InboundMediaConfigurer interface {
	SetInboundMediaGuard(InboundMediaGuard, func(context.Context, Platform, *Message))
}
