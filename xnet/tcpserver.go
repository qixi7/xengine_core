package xnet

import (
	"crypto/tls"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"sync"
	"sync/atomic"
	"xcore/xlog"
	"xcore/xmetric"
)

// Server类接口
type Server interface {
	Close()
	ProcessEvent() bool // 返回当前是否全部处理
	SetUserData(interface{})
}

// config
type ServerConfig struct {
	Network       string              // 用的什么网络
	Addr          string              // addr
	PostEvent     bool                // 是否push事件方式
	Handler       SessionEventHandler // 客户端连接事件回调
	Factory       StreamFactory       // 工厂类
	MaxSessionNum uint32              // 最大连接数
	QueueBufLen   int                 // 收发队列buf长度
	Cerificate    *tls.Certificate    // 通信加密配置
}

// tcp server
type server struct {
	*SessionMgr
	*ServerConfig
	listener  net.Listener       // 监听器实例
	userData  interface{}        // user data
	evChan    chan *SessionEvent // event channel
	closeOnce sync.Once
	closeSig  chan int
	t         string // 连接类型
	// for optimize
	creator sessionEventSyncCreator
}

// 检查配置, 赋予默认值
func checkServerConfig(cfg *ServerConfig) {
	if cfg.PostEvent {
		if cfg.QueueBufLen == 0 {
			cfg.QueueBufLen = 1024
		}
	}

	if cfg.MaxSessionNum <= 0 {
		cfg.MaxSessionNum = 1024
	}
}

// new tcp server
func NewTcpServer(cfg *ServerConfig) Server {
	checkServerConfig(cfg)
	l, err := net.Listen(cfg.Network, cfg.Addr)
	if err != nil {
		xlog.Errorf("liten error on %s, because: %v", cfg.Addr, err)
		return nil
	}
	xlog.InfoF(fmt.Sprintf("listen at tcp://%s", cfg.Addr))

	s := &server{
		SessionMgr:   &SessionMgr{maxSessionNum: cfg.MaxSessionNum},
		ServerConfig: cfg,
		listener:     l,
		closeSig:     make(chan int),
		evChan:       make(chan *SessionEvent, defaultEvChanSize),
		t:            "TCP",
	}

	go s.serve()

	return s
}

func (s *server) SetUserData(udata interface{}) {
	s.userData = udata
}

func (s *server) postOpenEvent(session *Session) {
	if s.PostEvent {
		ev := &SessionEvent{
			session: session,
			evType:  sessionOpen,
		}
		s.evChan <- ev
	} else {
		s.Handler.OnOpen(session, false)
	}
}

func (s *server) postCloseEvent(session *Session) {
	if s.PostEvent {
		ev := &SessionEvent{
			session: session,
			evType:  sessionClose,
		}
		s.evChan <- ev
	} else {
		s.Handler.OnClose(session)
	}
}

func (s *server) postMsgEvent(session *Session, binData []byte, pk *PBPacket) {
	// 这是多线程环境
	if s.PostEvent {
		ev := s.creator.NewSync_SessionEvent()
		ev.session = session
		ev.evType = sessionData
		ev.binData = binData
		ev.pkData = pk
		s.evChan <- ev
	} else {
		s.Handler.OnMessage(session, binData, pk)
	}
}

func (s *server) onSessionClose(c Sessioner) {
	s.DeleteSession(c)
}

// 开启服务
func (s *server) serve() {
	sessionCfg := &sessionConfig{
		async:  s.PostEvent,
		bufLen: s.QueueBufLen,
	}
	for {
		conn, err := s.listener.Accept() // transport layer: 1.reliability(TCP or KCP)
		if err != nil {
			select {
			case _, ok := <-s.closeSig:
				if !ok {
					return
				}
			default:
			}
			xlog.Errorf(fmt.Sprintf("accept error on: %s, error: %s", s.listener.Addr().String(), err))
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			return
		}
		go s.handshake(conn, sessionCfg)
	}
}

// 握手
func (s *server) handshake(conn net.Conn, sessionCfg *sessionConfig) {
	tcpConn := conn.(*net.TCPConn)
	_ = tcpConn.SetLinger(0)
	if s.Cerificate != nil {
		conn = tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{*s.Cerificate},
		}) // transport layer: 3.TLS(optional)
	}
	stream := s.Factory.NewStream(conn)

	session, err := s.CreateSession(conn, stream, sessionCfg)
	if err != nil {
		xlog.Errorf("err=%v", err)
		_ = conn.Close()
		return
	}
	session.AddCloseCallback(s.onSessionClose)
	s.postOpenEvent(session)
	s.serveOne(session)
}

// 服务一个session
func (s *server) serveOne(c *Session) {
	for {
		b, p, err, needPostMsg := c.Recv()
		if err != nil {
			errNetLog(err, fmt.Sprintf("[%s] stop serving session %d: %v", s.t, c.ID(), err))
			_ = c.Close()
			break
		}
		if needPostMsg {
			s.postMsgEvent(c, b, p)
		}
	}
	s.postCloseEvent(c)
}

// (主线程)处理连接事件
func (s *server) ProcessEvent() bool {
	batch := 1024
	for i := 0; i < batch; i++ {
		select {
		case ev := <-s.evChan:
			switch ev.evType {
			case sessionOpen:
				s.Handler.OnOpen(ev.session, false)
			case sessionClose:
				s.Handler.OnClose(ev.session)
			case sessionData:
				s.Handler.OnMessage(ev.session, ev.binData, ev.pkData)
			default:
				panic("xcore/xnet.server.ProcessEvent")
			}
		default:
			return true
		}
	}
	return false
}

func (s *server) Close() {
	s.closeOnce.Do(s.close)
}

func (s *server) close() {
	close(s.closeSig)
	s.listener.Close()
	s.SessionMgr.sessionMap.Range(func(key, value interface{}) bool {
		value.(*Session).Close()
		return true
	})
}

// --------------- 性能收集 ---------------

type SessionMetric struct {
	EventChanLen    float64
	SessionNum      float64
	SessionTotalNum float64
	SendChanMaxLen  float64
	SendChanMinLen  float64
	SendChanAvgLen  float64
	typename        string
}

func (m *SessionMetric) Pull(s Server, typename string) {
	m.typename = typename
	var realSrv *server
	switch srv := s.(type) {
	case *server:
		realSrv = srv
	case *UDPServer:
		realSrv = &srv.server
	case *RDPServer:
		realSrv = &srv.server
	default:
		return
	}
	m.EventChanLen = float64(len(realSrv.evChan))
	m.SessionNum = float64(atomic.LoadUint32(&realSrv.sessionNum))
	m.SessionTotalNum = float64(atomic.LoadUint64(&realSrv.sessionTotalNum))
	m.SendChanMaxLen, m.SendChanMinLen, m.SendChanAvgLen = 0, 0, 0
	realSrv.sessionMap.Range(func(aKey, aValue interface{}) bool {
		sess, ok := aValue.(*Session)
		if !ok || sess.sendChan == nil {
			return true
		}
		chanLen := float64(sess.sendChan.Len())
		if m.SendChanMaxLen < chanLen {
			m.SendChanMaxLen = chanLen
		}
		if m.SendChanMinLen == 0 || m.SendChanMinLen > chanLen {
			m.SendChanMinLen = chanLen
		}
		m.SendChanAvgLen += chanLen
		return true
	})
	if m.SessionNum > 0 {
		m.SendChanAvgLen /= m.SessionNum
	}
}

func (m *SessionMetric) Push(gather *xmetric.Gather, ch chan<- prometheus.Metric) {
	sessLabel := []string{"typename"}
	gather.PushGaugeMetric(ch, "game_xnet_session_evenchan_len", m.EventChanLen, sessLabel, m.typename)
	gather.PushGaugeMetric(ch, "game_xnet_session_num", m.SessionNum, sessLabel, m.typename)
	gather.PushCounterMetric(ch, "game_xnet_session_totalnum", m.SessionTotalNum, sessLabel, m.typename)
	gather.PushGaugeMetric(ch, "game_xnet_session_sendchan_maxlen", m.SendChanMaxLen, sessLabel, m.typename)
	gather.PushGaugeMetric(ch, "game_xnet_session_sendchan_minlen", m.SendChanMinLen, sessLabel, m.typename)
	gather.PushGaugeMetric(ch, "game_xnet_session_sendchan_avglen", m.SendChanAvgLen, sessLabel, m.typename)
}
