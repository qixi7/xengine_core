//go:build !linux

package rdpkit

import (
	"net"
	"sync/atomic"
)

func (c *XConnBatch) init() {
	c.recvBuf = make([]byte, mtu)
}

func (c *XConnBatch) ReadBatch() error {
	n, from, err := c.conn.ReadFrom(c.recvBuf)
	if err != nil {
		return err
	}
	// do something
	atomic.AddUint64(&metrics.UDPRecvBytes, uint64(n))
	atomic.AddUint64(&metrics.UDPRecvPackets, 1)
	// checksum
	c.recvFunc(c.recvBuf[:n], from.(*net.UDPAddr))

	return nil
}

func (c *XConnBatch) WriteBatch() error {
	nbytes := 0
	npkts := 0
	for k := range c.SendQ {
		//infoF("realSend, len=%d, l=%s, to=%s, send=%v",
		//	len(c.SendQ), c.conn.LocalAddr(), c.SendQ[k].Addr.String(), c.SendQ[k].Buffers[0])
		if n, err := c.conn.WriteTo(c.SendQ[k].Buffers[0], c.SendQ[k].Addr); err == nil {
			nbytes += n
			npkts++
		} else {
			errorF("WriteBatch err=%v", err)
			return err
		}
	}
	atomic.AddUint64(&metrics.UDPSendBytes, uint64(npkts))
	atomic.AddUint64(&metrics.UDPSendPackets, uint64(npkts))
	return nil
}
