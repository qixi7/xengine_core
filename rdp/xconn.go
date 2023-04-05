package rdpkit

import (
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net"
	"sync"
)

type batchConn interface {
	WriteBatch(ms []ipv4.Message, flags int) (int, error)
	ReadBatch(ms []ipv4.Message, flags int) (int, error)
}

type XConnBatch struct {
	// 只支持linux
	batch   batchConn
	recvQ   []ipv4.Message // read msg(只支持linux)
	recvBuf []byte         // 非linux recv buf
	SendQ   []ipv4.Message // write msg
	SendBf  []*buffer      // write msg
	conn    *net.UDPConn
	// data
	errPacketNum uint32 // 来源错误的包数
	// func
	recvFunc func(data []byte, addr *net.UDPAddr)
	// batchMutex
	mu sync.Mutex
}

func NewXConn(conn *net.UDPConn) *XConnBatch {
	var xconn batchConn
	addr, err := net.ResolveUDPAddr("udp", conn.LocalAddr().String())
	if err == nil {
		if addr.IP.To4() != nil {
			xconn = ipv4.NewPacketConn(conn)
		} else {
			xconn = ipv6.NewPacketConn(conn)
		}
	}

	if xconn == nil {
		errorF("NewXConn err=%v", err)
		return nil
	}

	c := &XConnBatch{
		conn:  conn,
		batch: xconn,
	}
	c.init()

	return c
}

func (c *XConnBatch) SetRecvLogic(f func(data []byte, addr *net.UDPAddr)) {
	c.recvFunc = f
}

// Uncork sends data in SendQ if there is any
func (c *XConnBatch) uncork() error {
	var err error
	c.mu.Lock()
	if len(c.SendQ) > 0 {
		err = c.WriteBatch()
		for k := range c.SendQ {
			c.SendQ[k].Buffers = nil
		}
		c.SendQ = c.SendQ[:0]
	}
	if len(c.SendBf) > 0 {
		// recycle
		for k := range c.SendBf {
			putBuffer(c.SendBf[k])
		}
		c.SendBf = c.SendBf[:0]
	}
	c.mu.Unlock()
	return err
}

func (c *XConnBatch) fillWriteQueue(packet []byte, size int, to *net.UDPAddr) {
	bf := getBuffer()
	copy(bf[:size], packet[:size])

	c.mu.Lock()
	var msg ipv4.Message
	msg.Buffers = [][]byte{bf[:size]}
	msg.Addr = to
	c.SendQ = append(c.SendQ, msg)
	c.SendBf = append(c.SendBf, bf)
	c.mu.Unlock()
}
