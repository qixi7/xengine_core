package rdp

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"net"
	"sync/atomic"
)

var (
	errChecksumMismatch = errors.New("checksum mismatch")
)

type ChecksumConn interface {
	net.PacketConn
	writeFullPacket([]byte, int, *net.UDPAddr) error
	writePacket([]byte, int, *net.UDPAddr) (int, error)
	readPacket(packet []byte) (n int, addr *net.UDPAddr, err error)
	Overhead() int
}

var (
	_ ChecksumConn = (*crcConn)(nil)
)

type crcConn struct {
	*net.UDPConn
}

// 用于校验的4字节. 使用checksum
func (crcConn) Overhead() int {
	return 4
}

func (c crcConn) sealPacket(packet []byte, n int) int {
	binary.BigEndian.PutUint32(packet[n:n+c.Overhead()], crc32.ChecksumIEEE(packet[:n])^magic)
	return n + c.Overhead()
}

func (c crcConn) openPacket(packet []byte, n int) int {
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

func (c *crcConn) writeFullPacket(packet []byte, size int, to *net.UDPAddr) error {
	n, err := c.writePacket(packet, size, to)
	if err != nil {
		return err
	}
	if n < size {
		return io.ErrShortWrite
	}
	return nil
}

func (c *crcConn) writePacket(packet []byte, size int, to *net.UDPAddr) (int, error) {
	if len(packet) < size+c.Overhead() {
		return 0, ErrMTUExceeded
	}
	size = c.sealPacket(packet, size)

	atomic.AddUint64(&metrics.UDPSendBytes, uint64(size))
	atomic.AddUint64(&metrics.UDPSendPackets, 1)

	return c.WriteToUDP(packet[:size], to)
}

// readPacket may modify buffer even when failed.
// A flooding-safe wrapper for ReadFromUDP
func (c *crcConn) readPacket(packet []byte) (n int, addr *net.UDPAddr, err error) {
	n, addr, err = c.ReadFromUDP(packet)
	if err != nil {
		return
	}

	atomic.AddUint64(&metrics.UDPRecvBytes, uint64(n))
	atomic.AddUint64(&metrics.UDPRecvPackets, 1)
	n = c.openPacket(packet, n)
	if n < 1 {
		err = errChecksumMismatch
		return
	}
	return
}
