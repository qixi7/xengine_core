//go:build linux

package rdpkit

import (
	"golang.org/x/net/ipv4"
	"net"
	"sync/atomic"
)

const (
	batchSize = 16
)

func (c *XConnBatch) init() {
	c.recvQ = make([]ipv4.Message, batchSize)
	for k := range c.recvQ {
		c.recvQ[k].Buffers = [][]byte{make([]byte, mtu)}
	}
}

func (c *XConnBatch) ReadBatch() error {
	var count int
	var recvBytes int
	var err error
	if count, err = c.batch.ReadBatch(c.recvQ, 0); err == nil {
		for i := 0; i < count; i++ {
			msg := &c.recvQ[i]
			recvBytes += msg.N
			// use data
			c.recvFunc(msg.Buffers[0][:msg.N], msg.Addr.(*net.UDPAddr))
		}
		atomic.AddUint64(&metrics.UDPRecvBytes, uint64(recvBytes))
		atomic.AddUint64(&metrics.UDPRecvPackets, uint64(count))
		return nil
	}
	// 不支持batch, 报错
	return err
}

func (c *XConnBatch) WriteBatch() error {
	// x/net version
	nbytes := 0
	npkts := 0
	var err error
	var n int
	for len(c.SendQ) > 0 {
		n, err = c.batch.WriteBatch(c.SendQ, 0)
		if err != nil {
			errorF("WriteBatch err=%v", err)
			return err
		}
		//infoF("writeBatchOnce, batchNum=%d, n=%d", len(c.SendQ), n)
		for k := range c.SendQ[:n] {
			nbytes += len(c.SendQ[k].Buffers[0])
		}
		npkts += n
		c.SendQ = c.SendQ[n:]
	}
	atomic.AddUint64(&metrics.UDPSendBytes, uint64(npkts))
	atomic.AddUint64(&metrics.UDPSendPackets, uint64(npkts))
	return nil
}
