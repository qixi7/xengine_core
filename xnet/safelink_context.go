package xnet

import (
	"bufio"
	"encoding/binary"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"xcore/xlog"
	"xcore/xmetric"
	"xcore/xnet/baserpcpb"
	"xcore/xutil"
)

const rpcHeartbeatDuration = time.Second * 3    // rpc心跳间隔
const LinkSignature uint64 = 0x6675636b7363616e // 连接签名(防止被其他程序误连接)
const (
	read2writeHeart = iota
	read2writeMax
)

// event 网络事件. 网络线程抛给主线程的event.
type evValue struct {
	ev   evKind       // 事件类型
	link *Link        // 连接实例
	bind net.Listener // lister
	err  error        // 错误信息
}

// 连接管理类上下文
type Context struct {
	ev             chan evValue            // 网络事件 event channel
	binds          map[string]net.Listener // 连接监听器map. key="ip+port", value=监听器
	links          map[*Link]struct{}      // link map
	isstop         bool                    // 是否调用了stop
	hdl            evHandler               // 业务层需要实现的网络事件event处理接口
	linkNum        int32                   // 连接数量
	remoteMap      map[int32]struct{}      // 远端remote map. key=remoteID, value=是否正在连接
	remoteMapMutex sync.Mutex              // remoteMap mutex
	dialMap        map[string]bool         // key->远端地址, value->是否需要继续dial
	dialMapMutex   sync.Mutex              // dialMap mutex
	evCache        ExpandQueueEvValue      // 记录没处理完的cache
	Timeout        time.Duration           // 读写超时时间
}

// new
func NewContext() *Context {
	ctx := &Context{
		ev:        make(chan evValue, chanElemNum),
		binds:     map[string]net.Listener{},
		links:     map[*Link]struct{}{},
		isstop:    false,
		hdl:       &normalHandler{},
		remoteMap: map[int32]struct{}{},
		dialMap:   make(map[string]bool),
	}
	return ctx
}

// 关闭所有listener 和建立好的links
func (ctx *Context) StopAll() {
	ctx.isstop = true
	for _, bind := range ctx.binds {
		bind.Close()
	}
	ctx.binds = map[string]net.Listener{}
	for link := range ctx.links {
		link.Close()
	}
}

// 连接一个目标机
func (ctx *Context) PostDial(addr string, wat Watcher, pkfmt PacketFormater, routeID int32, name string) {
	ctx.isstop = false
	ctx.hdl = &normalHandler{}
	go func() {
		var conn net.Conn
		var err error
		sleepTime := 100 * time.Millisecond
		// TODO: 如果ip地址是本机, 这里可以监听回环地址
		//if ipStr, portStr, err := net.SplitHostPort(addr); err == nil && netutil.IsLocalHostIP(ipStr) {
		//	addr = "127.0.0.1:" + portStr
		//}
		// 若dial存在 or 不用再继续dial了(从cluster配置中删除), 直接return
		ctx.addDialMap(addr)
		for {
			if !ctx.needDial(addr) {
				xlog.InfoF("PostDial addr=%s, need not dial now.", addr)
				return
			}
			conn, err = net.DialTimeout("tcp", addr, 1*time.Second)
			if ctx.isstop {
				if err == nil {
					ctx.delDialMap(addr)
					conn.Close()
				}
				return
			}
			if err == nil {
				ctx.delDialMap(addr)
				break
			}
			sleepTime += sleepTime * 2
			if sleepTime > 10*time.Second {
				sleepTime = 10 * time.Second
			}
			time.Sleep(sleepTime)
		}
		link := newLink(conn, wat, pkfmt, routeID, name, true)
		go ctx.setUpLink(link) // write thread
	}()
}

func (ctx *Context) Listen(addr string, wat Watcher, pkfmt PacketFormater, routeID int32, name string) error {
	ctx.isstop = false
	// 优化: 同时监听本机ip和回环地址(去掉ip)
	if _, portStr, err := net.SplitHostPort(addr); err == nil {
		addr = ":" + portStr
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		xlog.Errorf("listen tcp %v, meets err=%v", addr, err)
		return err
	}
	// save
	ctx.binds[ln.Addr().String()] = ln
	// listen thread
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				ctx.ev <- evValue{ev: evCloseBind, bind: ln, err: err}
				break
			}
			if ctx.isstop {
				conn.Close()
				break
			}
			link := newLink(conn, wat, pkfmt, routeID, name, false)
			go ctx.setUpLink(link) // write thread
		}
	}()
	return nil
}

// 建立连接
func (ctx *Context) setUpLink(link *Link) {
	var err error
	// rpc协议栈握手_第1次: set up
	headbuf := make([]byte, headSize)
	var buf []byte
	setup := &baserpcpb.MsgSetUp{
		Sign: LinkSignature,
		ID:   link.localID,
		Name: link.localName,
	}
	buf, err = setup.Marshal()
	if err != nil {
		xlog.Errorf("setUpLink, MarshalTo MsgSetUp err=%v", err)
		return
	}
	buf = append(headbuf, buf...)
	recv, ok := exchangeMessage(buf, ctx, link)
	if !ok {
		xlog.Errorf("setUpLink MsgSetUp err, close and redial.")
		link.conn.Close()
		ctx.redialIfDisconnect(link)
		return
	}
	func() {
		defer func() {
			if recover() != nil {
				setup.Sign = 0
			}
		}()
		err = setup.Unmarshal(recv)
		if err != nil {
			xlog.Errorf("setUpLink Unmarshal MsgSetUp err=%v", err)
			return
		}
	}()
	// 检测签名
	if setup.Sign != LinkSignature {
		xlog.Errorf("setUpLink setup.Sign != LinkSignature, close and redial.")
		link.conn.Close()
		ctx.redialIfDisconnect(link)
		return
	}

	link.remoteID = setup.ID
	link.remoteName = setup.Name
	remoteID := setup.ID

	ctx.remoteMapMutex.Lock()
	_, hasRemoteID := ctx.remoteMap[setup.ID]
	if !hasRemoteID {
		ctx.remoteMap[setup.ID] = struct{}{}
	}
	ctx.remoteMapMutex.Unlock()
	if hasRemoteID {
		xlog.Errorf("duplicate remote ID:%d closing", setup.ID)
		time.Sleep(time.Second) // make sure the packet is received by the remote peer
		link.conn.Close()
		ctx.redialIfDisconnect(link)
		return
	}
	defer func() {
		ctx.remoteMapMutex.Lock()
		delete(ctx.remoteMap, remoteID)
		ctx.remoteMapMutex.Unlock()
	}()

	read2write := initRead2Write()

	// push rpc连接上来事件
	ctx.ev <- evValue{ev: evOpen, link: link}
	go ctx.runRead(link, read2write) // read thread

	atomic.AddInt32(&ctx.linkNum, 1)

	heartTicker := time.NewTicker(rpcHeartbeatDuration)
	defer heartTicker.Stop()

sendThread:
	for {
		select {
		case _, ok := <-link.sendSig:
			if !ok {
				break sendThread
			}
			// send SafePacket
			link.sendb = link.sendb[:0]
			var pk SafePacket
			for link.sendq.Pop(&pk) {
				link.pkfmt.WritetoBuffer(pk, link)
				if len(link.sendb) > maxBufferSize && !link.closed {
					select {
					case link.sendSig <- struct{}{}:
					default:
					}
					break
				}
			}
			writeFull(link.conn, link.sendb)
		case <-heartTicker.C:
			if link.isdial {
				writeBuffer(link.conn, linkSerialHeart, heartSerialize())
			}
		case fn := <-read2write[read2writeHeart]:
			fn()
		}
	}

	atomic.AddInt32(&ctx.linkNum, -1)
	ctx.redialIfDisconnect(link)
}

// rpc: read
func (ctx *Context) runRead(link *Link, read2write [read2writeMax]chan func()) {
	var buf = make([]byte, bufferSize) // it can grow
	reader := bufio.NewReaderSize(link.conn, bufferSize)
	for {
		if ctx.Timeout > 0 {
			if err := link.conn.SetReadDeadline(time.Now().Add(ctx.Timeout)); err != nil {
				xlog.Errorf("<xnet_safelink> remote:%s_%d set dead line err=%v",
					link.remoteName, link.remoteID, err)
				break
			}
		}
		serial, tempBuf, err := readBuffer(reader, buf)
		if err != nil {
			break
		}
		if serial == linkSerialHeart {
			if link.isdial {
				link.RTT = heartDeserialize(tempBuf)
			} else {
				// receive heart, update to latest
				read2WriteSingleUpdate(read2write[read2writeHeart], func() {
					writeBuffer(link.conn, linkSerialHeart, tempBuf)
				})
			}
			continue
		}

		var pk SafePacket
		pk = link.pkfmt.ReadfromBuffer(tempBuf)
		if pk == nil {
			continue
		}
		link.recvq <- queueElem{serial: serial, pk: pk}
		if len(link.recvq) == 1 {
			ctx.ev <- evValue{ev: evMessage, link: link}
		}
		// 事件驱动
		if xutil.GApp.IsEvenDriverMode() { // 事件驱动模式下立即处理消息
			xutil.GApp.InvokeFuncOnMain()
		}
	}
	link.conn.Close()
	ctx.ev <- evValue{ev: evClose, link: link}
}

// rpc 协议栈握手
func exchangeMessage(buf []byte, ctx *Context, link *Link) ([]byte, bool) {
	binary.LittleEndian.PutUint32(buf, uint32(len(buf)-headSize))
	if err := writeFull(link.conn, buf); err != nil {
		xlog.Errorf("exchangeMessage phase 1:", err)
		return nil, false
	}
	if err := readFull(link.conn, buf[:headSize]); err != nil {
		xlog.Errorf("exchangeMessage phase 2: err=%v", err)
		return nil, false
	}
	size := binary.LittleEndian.Uint32(buf[:4]) // read 0-3
	if size < 1 || size > (1<<20) {
		xlog.Errorf("exchangeMessage phase 3: weired size:%v", size)
		return nil, false
	}
	recv := make([]byte, size)
	if err := readFull(link.conn, recv); err != nil {
		xlog.Errorf("exchangeMessage phase 4:", err)
		return nil, false
	}
	return recv, true
}

// 检查是否需要继续dial
func (ctx *Context) addDialMap(addr string) {
	ctx.dialMapMutex.Lock()
	ctx.dialMap[addr] = true
	ctx.dialMapMutex.Unlock()
	xlog.Debugf("add dial map addr=%s", addr)
}

// 删除dial map
func (ctx *Context) delDialMap(addr string) {
	ctx.dialMapMutex.Lock()
	delete(ctx.dialMap, addr)
	ctx.dialMapMutex.Unlock()
	xlog.Debugf("delDialMap addr=%s", addr)
}

// 检查是否需要继续dial
func (ctx *Context) needDial(addr string) bool {
	ctx.dialMapMutex.Lock()
	needDial, ok := ctx.dialMap[addr]
	if !ok {
		needDial = false
	}
	ctx.dialMapMutex.Unlock()
	return needDial
}

func initRead2Write() [read2writeMax]chan func() {
	var read2write [read2writeMax]chan func()
	read2write[read2writeHeart] = make(chan func(), 1)
	return read2write
}

// 根据不同信号处理不同函数
func read2WriteSingleUpdate(ch chan func(), fn func()) {
	select {
	case ch <- fn:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- fn:
		default:
		}
	}
}

// 重新dial断开的rpc连接
func (ctx *Context) redialIfDisconnect(link *Link) bool {
	if !link.isdial {
		return false
	}
	if link.callClose {
		// close the link
		return false
	}
	// disconnect, then auto reconnect
	xlog.InfoF("ReDial Addr=[%s]", link.conn.RemoteAddr().String())
	ctx.PostDial(link.conn.RemoteAddr().String(), link.wat, link.pkfmt, link.localID, link.localName)
	return true
}

// 处理事件
func (ctx *Context) dealEvent(ev evValue) {
	switch ev.ev {
	case evOpen:
		ctx.hdl.OnOpen(ctx, ev)
	case evClose:
		ctx.hdl.OnClose(ctx, ev)
	case evMessage:
		ctx.hdl.OnMessage(ctx, ev)
	case evCloseBind:
		xlog.InfoF("evCloseBind %s", ev.bind.Addr().String())
		delete(ctx.binds, ev.bind.Addr().String())
	default:
		panic(ev.ev)
	}
}

func (ctx *Context) Tick() {
	// 先处理未处理完的cache
	for ctx.evCache.Len() != 0 {
		ev := ctx.evCache.Pop()
		xlog.Debugf("Tick cache!!!! evType=%v, len(recvq)=%d, node=%s",
			ev.ev, len(ev.link.recvq), ev.link.remoteName)
		ctx.dealEvent(ev)
	}
	// 正常处理
	for {
		select {
		case ev := <-ctx.ev:
			// warning! too many!
			if ev.link != nil && len(ev.link.recvq) > 5000 {
				xlog.Debugf(" Tick normal!!!! evType=%v, len(recvq)=%d, node=%s",
					ev.ev, len(ev.link.recvq), ev.link.remoteName)
			}
			ctx.dealEvent(ev)
		default:
			return
		}
	}
}

// 删除监听
func (ctx *Context) CloseListen(addr string) {
	if ln, ok := ctx.binds[addr]; ok {
		ln.Close()
	}
}

// 删除dial
func (ctx *Context) CloseDialConnection(addr string) {
	for link := range ctx.links {
		if link.GetRemoteAddr() == addr {
			link.Close()
			xlog.InfoF("this node=%s, localAddr=%s should disconnect.",
				link.GetRemoteName(), link.GetLocalAddr())
			break
		}
	}
	ctx.delDialMap(addr)
}

// --------------- 性能收集 ---------------

type SafeLinkMetric struct {
	EventLen float64
}

func (m *SafeLinkMetric) Pull(ctx *Context) {
	m.EventLen = float64(len(ctx.ev) + ctx.evCache.Len())
}

func (m *SafeLinkMetric) Push(gather *xmetric.Gather, ch chan<- prometheus.Metric) {
	gather.PushGaugeMetric(ch, "game_xnet_safelink_evennum", m.EventLen, nil)
}
