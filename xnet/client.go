package xnet

/*
	client.go: 模拟客户端使用网络层的假客户端. 用于机器人测试
*/

import (
	"crypto/tls"
	"fmt"
	"github.com/qixi7/xengine_core/xlog"
	"net"
	"time"
)

type TransmitProto int

const (
	ProtocolTCP = TransmitProto(iota)
	ProtocolKCP
	ProtocolRDP
)

type ClientConfig struct {
	Protocol    TransmitProto
	Network     string
	Addr        string
	PostEvent   bool
	Handler     SessionEventHandler
	Factory     StreamFactory
	QueueBufLen int
	UserData    interface{}
	Timeout     time.Duration
	Secure      bool
}

func checkClientConfig(cfg *ClientConfig) {
	if cfg.PostEvent {
		if cfg.QueueBufLen == 0 {
			cfg.QueueBufLen = 1024
		}
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Second * 15
	}
}

func serveClient(c *Client) {
	for {
		b, p, err, needPostMsg := c.Recv()
		if err != nil {
			errNetLog(err, fmt.Sprintf("client[%d] closed, server addr: %s, err:%v",
				c.ID(), c.LocalAddr(), err))
			_ = c.Session.Close()
			break
		}
		if needPostMsg {
			c.postMsgEvent(c.Session, b, p)
		}
	}
	c.postCloseEvent(c.Session)
}

type Client struct {
	clientDialer
	*Session
	*ClientConfig
	evChan    chan *SessionEvent
	reopen    bool
	destroyed bool
	// for optimize
	creator sessionEventSyncCreator
}

func (c *Client) postOpenEvent(session *Session) {
	if c.PostEvent {
		ev := &SessionEvent{
			session: session,
			evType:  sessionOpen,
		}
		c.evChan <- ev
	} else {
		c.Handler.OnOpen(session, false)
	}
}

func (c *Client) postReopenEvent(session *Session) {
	if c.PostEvent {
		ev := &SessionEvent{
			session: session,
			evType:  sessionReopen,
		}
		c.evChan <- ev
	} else {
		c.Handler.OnOpen(session, true)
	}
}

func (c *Client) postCloseEvent(session *Session) {
	if c.PostEvent {
		ev := &SessionEvent{
			session: session,
			evType:  sessionClose,
		}
		c.evChan <- ev
	} else {
		c.Handler.OnClose(session)
	}
}

func (c *Client) postMsgEvent(session *Session, binData []byte, pk *PBPacket) {
	if c.PostEvent {
		ev := c.creator.NewSync_SessionEvent()
		ev.session = session
		ev.evType = sessionData
		ev.binData = binData
		ev.pkData = pk
		c.evChan <- ev
	} else {
		//c.Handler.OnMessage(session, msg)
	}
}

func (c *Client) ProcessEvent() bool {
	batch := 1024
	for i := 0; i < batch; i++ {
		select {
		case ev := <-c.evChan:
			switch ev.evType {
			case sessionOpen:
				c.Handler.OnOpen(ev.session, false)
			case sessionReopen:
				c.Handler.OnOpen(ev.session, true)
			case sessionClose:
				c.Handler.OnClose(ev.session)
			case sessionData:
				c.Handler.OnMessage(ev.session, ev.binData, ev.pkData)
			default:
				panic("xcore/xnet.Client.ProcessEvent")
			}
		default:
			return true
		}
	}
	return false
}

func (c *Client) Close() {
	if c.destroyed {
		return
	}
	_ = c.Session.Close()
	c.destroyed = true
}

func (c *Client) keepConnect() {
	//for !c.destroyed {
	for !c.connect() {
		time.Sleep(5 * time.Second)
	}
	if c.destroyed {
		return
	}
	//xlog.InfoF("connect: %s %s", c.Network, c.Addr)
	if c.reopen {
		c.postReopenEvent(c.Session)
	} else {
		c.postOpenEvent(c.Session)
		c.reopen = true
	}
	serveClient(c)
	//}
}

type clientDialer interface {
	connect() bool
}

type tcpDialer struct {
	c *Client
}

func (d *tcpDialer) connect() (done bool) {
	c := d.c
	if c.destroyed {
		return true
	}
	var conn net.Conn
	var err error
	if c.Timeout > 0 {
		conn, err = net.DialTimeout(c.Network, c.Addr, c.Timeout)
	} else {
		conn, err = net.Dial(c.Network, c.Addr)
	}
	if err != nil {
		xlog.Errorf("connect failed: %s %s %v", c.Network, c.Addr, err)
		return false
	}
	if c.destroyed {
		_ = conn.Close()
		return true
	}

	if c.ClientConfig.Secure {
		conn = tls.Client(conn, &tls.Config{
			InsecureSkipVerify: true,
		})
		if err := conn.(*tls.Conn).Handshake(); err != nil {
			xlog.Errorf("tls handshake failed: ", err)
			return false
		}
		if c.destroyed {
			_ = conn.Close()
			return true
		}
	}

	if err := c.ReBind(conn, c.Factory.NewStream(conn)); err != nil {
		xlog.Errorf("rebind failed: ", err)
		return false
	}
	return true
}

func PostDial(cfg *ClientConfig) (*Client, error) {
	checkClientConfig(cfg)
	sessionCfg := &sessionConfig{
		async:  cfg.PostEvent,
		bufLen: cfg.QueueBufLen,
	}
	cli := &Client{
		ClientConfig: cfg,
		Session:      newClientSession(sessionCfg),
		evChan:       make(chan *SessionEvent, defaultEvChanSize),
	}

	switch cfg.Protocol {
	case ProtocolTCP:
		cli.clientDialer = &tcpDialer{cli}
	case ProtocolKCP:
		cli.clientDialer = &kcpDialer{cli}
	case ProtocolRDP:
		cli.clientDialer = &rdpDialer{cli}
	default:
		panic("unKnown protocol in PostDial")
	}
	cli.SetUserData(cfg.UserData)
	go cli.keepConnect()
	return cli, nil
}
