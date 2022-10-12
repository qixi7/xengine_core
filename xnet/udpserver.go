package xnet

import (
	"crypto/tls"
	"fmt"
	"github.com/qixi7/xengine_core/xlog"
	"github.com/xtaci/kcp-go"
	"net"
	"time"
)

// UDP server. 现主要用kcp
type UDPServer struct {
	server
}

// new
func NewUDPServer(cfg *ServerConfig) Server {
	checkServerConfig(cfg)

	l, err := kcp.Listen(cfg.Addr)
	if err != nil {
		xlog.Errorf("listen error on %s, err=%v", cfg.Addr, err)
		return nil
	}
	xlog.InfoF(fmt.Sprintf("listen on udp://%s", cfg.Addr))

	s := &UDPServer{
		server: server{
			SessionMgr:   &SessionMgr{maxSessionNum: cfg.MaxSessionNum},
			ServerConfig: cfg,
			listener:     l,
			closeSig:     make(chan int),
			evChan:       make(chan *SessionEvent, defaultEvChanSize),
			t:            "UDP",
		},
	}

	go s.serveUDP()
	return s
}

func (s *UDPServer) serveUDP() {
	sessionCfg := &sessionConfig{
		async:  s.PostEvent,
		bufLen: s.QueueBufLen,
	}
	tempDelay := time.Duration(0)
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
				if tempDelay == 0 {
					tempDelay = 5 * time.Second
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
			}
			time.Sleep(tempDelay)
			continue
		}
		go s.handshakeUDP(conn, sessionCfg)
	}
}

func (s *UDPServer) handshakeUDP(conn net.Conn, sessionCfg *sessionConfig) {
	kcpConn := conn.(*kcp.UDPSession)
	//kcpConn.SetNoDelay(1, 10, 2, 1)
	kcpConn.SetNoDelay(0, 40, 0, 0)
	kcpConn.SetStreamMode(true)
	kcpConn.SetWindowSize(256, 256)
	// According to IEEE 802.3 & IEEE 802.11, 1492 bytes is The maximum MTU for ethernet and WIFI.
	// UDP/IPv4 overhead is 28 bytes per packet. To prevent low level packet splitting,
	// it should be 1492 - 28 = 1464 bytes, this must be bigger than SubPackageSize+minPacketSize.
	// In case of it isn't, reduce SubPackageSize
	kcpConn.SetMtu(1200)
	// No proxy support for kcp
	if s.Cerificate != nil {
		conn = tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{*s.Cerificate},
		}) // transport layer: 2.TLS(optional)
	}

	stream := s.Factory.NewStream(conn)
	session, err := s.CreateSession(conn, stream, sessionCfg)
	if err != nil {
		xlog.Error(err)
		_ = conn.Close()
		return
	}
	session.AddCloseCallback(s.onSessionClose)
	s.postOpenEvent(session)
	s.serveOne(session)
}
