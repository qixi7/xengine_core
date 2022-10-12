package xnet

import (
	"fmt"
	"github.com/qixi7/xengine_core/rdp"
	"github.com/qixi7/xengine_core/xlog"
	"net"
	"time"
)

// rdp server context
type RDPServer struct {
	server
}

func NewRDPServer(cfg *ServerConfig, timeout time.Duration) Server {
	checkServerConfig(cfg)

	l, err := rdp.Listen(cfg.Addr)
	if err != nil {
		xlog.Errorf(fmt.Sprintf("listen error on %s, because %s", cfg.Addr, err))
		return nil
	}
	l.SetClientTimeout(timeout)
	xlog.InfoF("listen on rdp://%s, timeout=%v", cfg.Addr, timeout)

	s := &RDPServer{
		server: server{
			SessionMgr:   &SessionMgr{maxSessionNum: cfg.MaxSessionNum},
			ServerConfig: cfg,
			listener:     l,
			closeSig:     make(chan int),
			evChan:       make(chan *SessionEvent, defaultEvChanSize),
			creator:      sessionEventSyncCreator{},
			t:            "RDP",
		},
	}
	go s.serveRDP()

	return s
}

func (s *RDPServer) serveRDP() {
	sessionCfg := &sessionConfig{
		async:  s.PostEvent,
		bufLen: s.QueueBufLen,
	}
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case _, ok := <-s.closeSig:
				if !ok {
					return
				}
			default:
			}
			xlog.Errorf("accept error on : %s, error=%v", s.listener.Addr().String(), err)
			return
		}
		go s.handshakeRDP(conn, sessionCfg)
	}
}

func (s *RDPServer) handshakeRDP(conn net.Conn, sessionCfg *sessionConfig) {
	stream := s.Factory.NewStreamWithDatagramConn(conn, rdp.MaxPacketSize)
	session, err := s.CreateSession(conn, stream, sessionCfg)
	if err != nil {
		_ = conn.Close()
		xlog.Errorf("handshakeRDP CreateSession err=%v", err)
		return
	}
	session.AddCloseCallback(s.onSessionClose)
	s.postOpenEvent(session)
	s.serveOne(session)
}
