package xnet

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"unsafe"
	"xcore/xcontainer/channel"
)

var (
	allocSessionID      uint32
	errSendBlocked      = errors.New("send chan blocked")
	errSessionNotClosed = errors.New("session not closed")
	errSessionClosed    = errors.New("session closed")
	errClosedLock       = errors.New("session close lock")
	errAsyncNotEnable   = errors.New("async not enable")
	errNotSupported     = errors.New("operation not supported")
)

// 客户端连接事件
const (
	sessionOpen = iota
	sessionReopen
	sessionClose
	sessionData

	defaultEvChanSize = 1024
)

type sessionConfig struct {
	async  bool
	bufLen int
}

// Sessioner Session interface
type Sessioner interface {
	ID() uint32
	RemoteAddr() string
	LocalAddr() string
	Recv() ([]byte, *PBPacket, error, bool)
	Close() error
	SyncSendRaw([]byte) error
	SyncSend(interface{}) error
	AsyncSend(interface{}) error
	GetUserData() interface{}
	SetUserData(interface{})
}

// 客户端连接事件回调处理
type SessionEventHandler interface {
	OnOpen(Sessioner, bool)
	OnClose(Sessioner)
	OnMessage(Sessioner, []byte, *PBPacket)
}

type SessionEvent struct {
	session Sessioner
	evType  int
	//data    interface{}
	binData []byte
	pkData  *PBPacket
}

type sessionEventSyncCreator struct {
	slice []SessionEvent
	idx   int
	mutex sync.Mutex
}

func (cr *sessionEventSyncCreator) NewSync_SessionEvent() *SessionEvent {
	cr.mutex.Lock()
	if cr.idx >= len(cr.slice) {
		cr.mutex.Unlock()
		slice := make([]SessionEvent, 4096, 4096)
		cr.mutex.Lock()
		if cr.idx >= len(cr.slice) {
			cr.slice = slice
			cr.idx = 0
		}
	}
	ele := &cr.slice[cr.idx]
	cr.idx++
	cr.mutex.Unlock()
	return ele
}

type Session struct {
	*sessionConfig
	id             uint32
	stream         unsafe.Pointer
	sendChan       *channel.UnBlockSPSC
	closeCallbacks []func(Sessioner)
	userData       interface{}
	local, remote  net.Addr
	sendSig        chan struct{}
}

func newSession(conn net.Conn, stream Streamer, cfg *sessionConfig) *Session {
	if cfg == nil {
		panic("sessionConfig is nil")
	}
	s := &Session{
		id:            atomic.AddUint32(&allocSessionID, 1),
		sessionConfig: cfg,
		sendSig:       make(chan struct{}, 1),
	}
	if stream != nil {
		s.stream = unsafe.Pointer(&stream)
	}
	if conn != nil {
		s.local = conn.LocalAddr()
		s.remote = conn.RemoteAddr()
	}
	if cfg.async {
		s.sendChan = &channel.UnBlockSPSC{}
		s.sendChan.Init(cfg.bufLen)
		go s.sender()
	}
	return s
}

// for robot test
func newClientSession(cfg *sessionConfig) *Session {
	s := newSession(nil, nil, cfg)
	return s
}

func (s *Session) ID() uint32 {
	return s.id
}

func (s *Session) RemoteAddr() string {
	return s.remote.String()
}

func (s *Session) LocalAddr() string {
	return s.local.String()
}

func (s *Session) ReBind(conn net.Conn, stream Streamer) (err error) {
	if stream == nil {
		panic("argument is nil")
	}
	local := conn.LocalAddr()
	remote := conn.RemoteAddr()

	defer func() {
		if p := recover(); p != nil {
			err = errors.New(fmt.Sprint(p))
		}
	}()

	if !atomic.CompareAndSwapPointer(&s.stream, nil, unsafe.Pointer(&stream)) {
		return errSessionNotClosed
	}

	s.local = local
	s.remote = remote
	if s.sendChan != nil {
		select {
		case s.sendSig <- struct{}{}:
		default:
		}
	}
	return nil
}

// add 关闭连接回调
func (s *Session) AddCloseCallback(cb func(Sessioner)) {
	s.closeCallbacks = append(s.closeCallbacks, cb)
}

func (s *Session) invokeCloseCallback() {
	for _, cb := range s.closeCallbacks {
		cb(s)
	}
}

// recv 消息
func (s *Session) Recv() ([]byte, *PBPacket, error, bool) {
	stream := s.getStream()
	if stream == nil {
		return nil, nil, errSessionClosed, false
	}
	return stream.Recv()
}

// close
func (s *Session) Close() (err error) {
	p := (*Streamer)(atomic.SwapPointer(&s.stream, nil))
	if p == nil {
		return
	}
	stream := *p
	if stream == nil {
		return
	}
	err = stream.Close()

	s.invokeCloseCallback()

	s.destroy()
	return
}

func (s *Session) destroy() {
	close(s.sendSig)
	if s.sessionConfig.async {
		s.sendChan.Close()
	}
}

// SyncSendRaw gorouteine safe, write net.Conn without encode
func (s *Session) SyncSendRaw(msg []byte) error {
	stream := s.getStream()
	if stream == nil {
		return errSessionClosed
	}
	if err := stream.SendRaw(msg); err != nil {
		_ = s.Close()
		return err
	}
	return nil
}

// SyncSend gorouteine safe
func (s *Session) SyncSend(msg interface{}) error {
	stream := s.getStream()
	if stream == nil {
		return errSessionClosed
	}
	return stream.Send(msg)
}

// ASyncSend main gorouteine only
func (s *Session) AsyncSend(msg interface{}) error {
	if !s.async {
		return errAsyncNotEnable
	}
	s.sendChan.Write(msg)
	return nil
}

func (s *Session) sender() {
	for {
		msg, ok := s.sendChan.Read()
		if !ok {
			return
		}
		for {
			stream := s.getStream()
			if stream == nil {
				if _, ok := <-s.sendSig; !ok {
					return
				}
				continue
			}
			if err := stream.Send(msg); err != nil {
				errNetLog(err, fmt.Sprintf("sender Send msg err=%v", err))
				_ = s.Close()
				continue
			}
			break
		}
	}
}

func (s *Session) getStream() Streamer {
	p := (*Streamer)(atomic.LoadPointer(&s.stream))
	if p == nil {
		return nil
	}
	return *p
}

func (s *Session) GetUserData() interface{} {
	return s.userData
}

func (s *Session) SetUserData(ud interface{}) {
	s.userData = ud
}
