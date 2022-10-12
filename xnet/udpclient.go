package xnet

import (
	"crypto/tls"
	"github.com/qixi7/xengine_core/rdp"
	"github.com/qixi7/xengine_core/xlog"
	"github.com/xtaci/kcp-go"
)

/**
udpclient.go: 主要用于机器人模拟客户端
*/

type kcpDialer struct {
	c *Client
}

func (d *kcpDialer) connect() (done bool) {
	c := d.c
	if c.destroyed {
		return true
	}
	conn, err := kcp.Dial(c.ClientConfig.Addr)
	if err != nil {
		return false
	}
	kcpConn, ok := conn.(*kcp.UDPSession)
	if !ok {
		panic("wrong udp connect type")
	}
	//kcpConn.SetNoDelay(1, 10, 2, 1)
	kcpConn.SetNoDelay(0, 40, 0, 0)
	kcpConn.SetStreamMode(true)
	kcpConn.SetWindowSize(256, 256)
	kcpConn.SetMtu(1200)
	_ = kcpConn.SetDSCP(46)

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

type rdpDialer struct {
	c *Client
}

func (d *rdpDialer) connect() (done bool) {
	c := d.c
	if c.destroyed {
		return true
	}
	conn, err := rdp.DialTimeout(c.ClientConfig.Network, c.ClientConfig.Addr, c.ClientConfig.Timeout)
	if err != nil {
		xlog.Errorf("RDP Dial err=%v", err)
		return false
	}
	if c.destroyed {
		_ = conn.Close()
		return true
	}
	if err := c.ReBind(conn, c.Factory.NewStreamWithDatagramConn(conn, rdp.MaxPacketSize)); err != nil {
		xlog.Errorf("rebind failed: ", err)
		return false
	}
	return true
}
