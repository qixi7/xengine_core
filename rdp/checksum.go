package rdpkit

import (
	"encoding/binary"
	"errors"
	"golang.org/x/net/ipv4"
	"hash/crc32"
	"net"
	"sync"
	"sync/atomic"
)

var (
	errChecksumMismatch = errors.New("checksum mismatch")
	crcOverhead         = 4
)

type ChecksumConn interface {
	net.PacketConn
	writeDirect(packet []byte, size int, to *net.UDPAddr) error
	batchFill(packet []byte, size int, to *net.UDPAddr) error
	batchFlush() error
	GetXConn() *XConnBatch
	Overhead() int
}

var (
	_ ChecksumConn = (*crcConn)(nil)
)

type crcConn struct {
	*net.UDPConn
	xConn           *XConnBatch
	mu              sync.Mutex
	recvMsgCallBack func([]byte, *net.UDPAddr)
}

func putCheckSum(packet []byte, n int) int {
	if len(packet) < n+crcOverhead {
		return 0
	}
	binary.BigEndian.PutUint32(packet[n:n+crcOverhead], crc32.ChecksumIEEE(packet[:n])^magic)
	return n + crcOverhead
}

func checkPacket(packet []byte, n int) int {
	p := n - crcOverhead
	if p <= 0 {
		return 0
	}
	// 校验body
	if binary.BigEndian.Uint32(packet[p:n]) != crc32.ChecksumIEEE(packet[:p])^magic {
		return 0
	}
	return p
}

func newCrcConn(conn *net.UDPConn, recv func([]byte, *net.UDPAddr)) *crcConn {
	c := &crcConn{
		UDPConn:         conn,
		recvMsgCallBack: recv,
	}
	c.xConn = NewXConn(conn)
	c.xConn.SetRecvLogic(c.recvCrcData)
	return c
}

func (c *crcConn) recvCrcData(data []byte, addr *net.UDPAddr) {
	n := checkPacket(data, len(data))
	if n < 1 {
		// 包错误
		errorF("readLogic checkSum err, [%d]data=%v", len(data), data)
		atomic.AddUint64(&metrics.CheckSumErr, uint64(1))
		return
	}
	c.recvMsgCallBack(data[:n], addr)
}

// 用于校验的4字节. 使用checksum
func (c *crcConn) Overhead() int {
	return crcOverhead
}

func (c *crcConn) GetXConn() *XConnBatch {
	return c.xConn
}

func (c *crcConn) checkPacket(packet []byte, n int) int {
	p := n - c.Overhead()
	if p <= 0 {
		return 0
	}
	// 校验body
	if binary.BigEndian.Uint32(packet[p:n]) != crc32.ChecksumIEEE(packet[:p])^magic {
		return 0
	}
	return p
}

// 直接发
func (c *crcConn) writeDirect(packet []byte, size int, to *net.UDPAddr) error {
	size = putCheckSum(packet, size)
	if size == 0 {
		return ErrMTUExceeded
	}
	var msg ipv4.Message
	msg.Buffers = [][]byte{packet[:size]}
	msg.Addr = to
	c.mu.Lock()
	c.xConn.SendQ = append(c.xConn.SendQ, msg)
	err := c.xConn.uncork()
	c.mu.Unlock()
	return err
}

// 延后发
func (c *crcConn) batchFill(packet []byte, size int, to *net.UDPAddr) error {
	size = putCheckSum(packet, size)
	if size == 0 {
		return ErrMTUExceeded
	}
	c.xConn.fillWriteQueue(packet, size, to)
	return nil
}

func (c *crcConn) batchFlush() error {
	return c.xConn.uncork()
}
