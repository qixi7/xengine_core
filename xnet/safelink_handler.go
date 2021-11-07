package xnet

type evKind int

const (
	evOpen      evKind = iota + 1 // 有新连接上来
	evReopen                      // 连接重连上来
	evClose                       // 连接关闭
	evMessage                     // 网络消息
	evCloseBind                   // 连接错误
)

// 网络消息处理 handler
type evHandler interface {
	OnOpen(ctx *Context, ev evValue)
	OnReopen(ctx *Context, ev evValue)
	OnClose(ctx *Context, ev evValue)
	OnMessage(ctx *Context, ev evValue)
}

// 常规rpc handler(实现evHandler)
type normalHandler struct {
}

func (hdl *normalHandler) OnOpen(ctx *Context, ev evValue) {
	ctx.links[ev.link] = struct{}{}
	ev.link.wat.OnOpen(ev.link, ev.udata)
}

func (hdl *normalHandler) OnReopen(ctx *Context, ev evValue) {
	ctx.links[ev.link] = struct{}{}
	ev.link.wat.OnReopen(ev.link, ev.udata)
}

func (hdl *normalHandler) OnClose(ctx *Context, ev evValue) {
	link := ev.link
	delete(ctx.links, link)
	link.wat.OnClose(link)
	link.closed = true
	close(link.sendSig)
}

func (hdl *normalHandler) OnMessage(ctx *Context, ev evValue) {
	const onePass = 1 << 16
	i := 0
	var serial int64
	link := ev.link
	if link.closed {
		return
	}
pass:
	for ; i < onePass; i++ {
		select {
		case elem := <-link.recvq:
			ev.link.wat.OnMessage(link, elem.pk)
			ev.link.pkfmt.DeprecateReadPacket(elem.pk)
			serial = elem.serial
		default:
			break pass
		}
	}
	// 有可能没处理完, 缓存着
	if i == onePass {
		ctx.evCache.Push(ev)
	}
	// 有消息被处理了, 发送ack信号
	if i > 0 {
		select {
		case link.ackq <- serial:
		default:
			select {
			case link.ackq <- serial:
			default:
			}
		}
	}
}
