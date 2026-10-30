package wecom

import (
	"context"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

type admittedWSMedia struct {
	admission *core.InboundMediaAdmission
	message   *core.Message
}

func (p *WSPlatform) SetInboundMediaGuard(guard core.InboundMediaGuard, handler func(context.Context, core.Platform, *core.Message)) {
	p.mediaGuard, p.mediaHandler = guard, handler
}

func (p *WSPlatform) mediaContext() context.Context {
	if p.ctx != nil {
		return p.ctx
	}
	return context.Background()
}

func (p *WSPlatform) mediaMessage(body *wsMsgCallbackBody, sessionKey, chatName string, rctx wsReplyContext, current, quoted wsInboundParts, fromVoice bool) *core.Message {
	return &core.Message{
		SessionKey: sessionKey, Platform: "wecom", MessageID: body.MsgID,
		UserID: body.From.UserID, UserName: body.From.UserID,
		ChatName: chatName, ChannelID: body.ChatID,
		Content:      stripWeComAtMentions(strings.Join(current.content, "\n"), p.botID, body.AibotID),
		ExtraContent: formatWSQuotedContent(quoted), ReplyCtx: rctx, FromVoice: fromVoice,
	}
}

func (p *WSPlatform) mediaFailure(rctx wsReplyContext, err error) {
	if p.mediaContext().Err() == nil {
		_ = p.Reply(p.mediaContext(), rctx, p.mediaGuard.MediaFailure(err))
	}
}

func (p *WSPlatform) dispatchWSMediaInbound(body *wsMsgCallbackBody, sessionKey, chatName string, rctx wsReplyContext, current, quoted wsInboundParts, fromVoice bool) {
	if p.mediaGuard == nil {
		go p.deliverWSMediaInbound(body, sessionKey, chatName, rctx, current, quoted, fromVoice)
		return
	}
	msg := p.mediaMessage(body, sessionKey, chatName, rctx, current, quoted, fromVoice)
	count := len(current.images) + len(current.files) + len(quoted.images) + len(quoted.files)
	// Admission must precede goroutine creation, HTTP and buffer allocation.
	// The same lock prevents Add racing with Stop's Wait.
	p.mediaMu.Lock()
	if p.mediaStopped || p.mediaHandler == nil {
		p.mediaMu.Unlock()
		return
	}
	admission, err := p.mediaGuard.AdmitMedia(p.mediaContext(), msg, count)
	if err != nil {
		p.mediaMu.Unlock()
		p.mediaFailure(rctx, err)
		return
	}
	p.mediaWG.Add(1)
	p.mediaMu.Unlock()
	go func() {
		defer p.mediaWG.Done()
		if admission == nil {
			p.mediaHandler(p.mediaContext(), p, msg)
			return
		}
		defer admission.Close()
		p.deliverWSMediaInbound(body, sessionKey, chatName, rctx, current, quoted, fromVoice, &admittedWSMedia{admission, msg})
	}()
}

var _ core.InboundMediaConfigurer = (*WSPlatform)(nil)
