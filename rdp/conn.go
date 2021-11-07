package rdp

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type incomingPacket struct {
	header packetHeader
	body   *buffer
	size   int
}

func (i *incomingPacket) slice() []byte {
	return i.body[:i.size]
}

func (i *incomingPacket) free() {
	if i.body != nil {
		putBuffer(i.body)
		i.body = nil
	}
}

type Conn struct {
	read       chan incomingPacket
	dispatcher dispatcher
	remote     *net.UDPAddr
	send       sendQueue
	recv       recvQueue
	seq        uint32
	readMu     chan struct{}
	reading    bool
	lastSeen   time.Time
	w          Window

	readDeadline  time.Time
	writeDeadline time.Time
	closeMu       sync.Mutex
	isClosed      bool
}

func (c *Conn) Read(b []byte) (n int, err error) {
	c.readMu <- struct{}{}
	defer func() { <-c.readMu }()

	if !c.readDeadline.IsZero() {
		ctx, cancel := context.WithDeadline(context.Background(), c.readDeadline)
		defer cancel()
		return c.readContext(ctx, b)
	}
	return c.readContext(context.Background(), b)
}

func (c *Conn) readContext(ctx context.Context, b []byte) (n int, err error) {
	receiveAny := false

	for {
		if load, ok := c.recv.Read(); ok {
			n = copy(b, load.slice())
			_ = c.sendAckByRead()
			load.free()
			c.lastSeen = time.Now()

			atomic.AddUint64(&metrics.RecvBytes, uint64(n))
			atomic.AddUint64(&metrics.RecvPackets, 1)
			return
		} else if receiveAny {
			receiveAny = false
			_ = c.sendAckBySelectAck()
		}

		// 直接取下一个seq失败, 等待recv新包
		select {
		case <-ctx.Done():
			return 0, context.DeadlineExceeded
		case p, ok := <-c.read:
			if !ok {
				return 0, io.ErrClosedPipe
			}
			receiveAny = c.transferToQueue(p) >= 3 && p.header.packetKind == packetData
		}
	}
}

func (c *Conn) transferToQueue(p incomingPacket) int {
	setSuccessNum := 0
	switch p.header.packetKind {
	case packetDial:
		_ = c.sendDialAck()
	case packetData:
		b := p.slice()
		load := packetLoad{buf: getBuffer()}
	ReadLoop:
		for {
			n := load.readFrom(b)
			if n == 0 {
				load.free()
				break ReadLoop
			}
			// 有load Set失败. 那么也相当于receiveAny==false
			if c.recv.Set(load) {
				setSuccessNum++
				load.buf = getBuffer()
			}
			b = b[n:]
		}
		p.free()
	case packetAck:
		c.send.Ack(p.header.ack, p.header.ackBits, &c.w)
	}
	p.free()

	return setSuccessNum
}

func (c *Conn) Write(b []byte) (int, error) {
	if !c.writeDeadline.IsZero() {
		ctx, cancel := context.WithDeadline(context.Background(), c.writeDeadline)
		defer cancel()
		return c.writeContext(ctx, b)
	}
	return c.writeContext(context.Background(), b)
}

func (c *Conn) writeContext(ctx context.Context, b []byte) (int, error) {
	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]
	loadBuf := getBuffer()
	// packet = packetHeader + packetLoad
	n := packetHeader{packetKind: packetData}.writeTo(packet)

	if len(b) > maxLoadSize {
		return 0, ErrMaxLoadSizeExceeded
	}

	load := packetLoad{
		seq:  c.seq,
		size: copy(loadBuf[:], b),
		buf:  loadBuf,
	}

	loadLen := load.writeTo(packet[n:])
	n += loadLen

	nowUnixNano := now()
	load.timestamp = nowUnixNano
	ticker := (*time.Ticker)(nil)
	for !c.send.Write(load) {
		// 没有位置了, 从queue里腾些位置出来. 先发次发送冗余, 等待收到ack以清理出空间
		_ = c.flush()
		if ticker == nil {
			ticker = time.NewTicker(40 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			ticker.Stop()
			return 0, context.DeadlineExceeded
		case c.readMu <- struct{}{}:
			// 因为此时有可能也收到了数据包
			select {
			case <-ctx.Done():
				<-c.readMu
				ticker.Stop()
				return 0, context.DeadlineExceeded
			case p, ok := <-c.read:
				if !ok {
					<-c.readMu
					ticker.Stop()
					return 0, io.ErrClosedPipe
				}
				c.transferToQueue(p)
			}
			<-c.readMu
		case <-ticker.C:
		}
	}
	if ticker != nil {
		ticker.Stop()
	}

	minRTT := c.w.Min()
	for pack := uint32(0); pack < RedundantNum; pack++ {
		p, ok := c.send.Get(pack)
		if !ok {
			continue
		}
		if !after(p.seq, load.seq) || n+p.overhead()+p.size+c.dispatcher.Overhead() > mtu {
			break
		}
		if (nowUnixNano - p.timestamp) < minRTT {
			continue
		}

		n += p.writeTo(packet[n:])
	}

	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.dispatcher.SetWriteDeadline(deadline)
		}
	}
	err := c.dispatcher.writeFullPacket(packet, n, c.remote)
	if err != nil {
		c.send.Clear(c.seq)
		return 0, err
	}

	atomic.AddUint64(&metrics.SendBytes, uint64(len(b)))
	atomic.AddUint64(&metrics.SendPackets, 1)

	c.seq++

	return len(b), nil
}

func (c *Conn) flush() error {
	atomic.AddInt64(&metrics.WriteBlockRetransmit, 1)

	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]

	n := packetHeader{packetKind: packetData}.writeTo(packet)

	writtenNum := 0
	for offsetPack := uint32(0); offsetPack < queueSize; offsetPack++ {
		p, ok := c.send.Get(offsetPack)
		if !ok {
			continue
		}
		if !after(p.seq, c.seq) || n+p.overhead()+p.size+c.dispatcher.Overhead() > mtu {
			break
		}
		n += p.writeTo(packet[n:])
		writtenNum++
		if writtenNum >= RedundantNum {
			break
		}
	}

	if writtenNum > 0 {
		err := c.dispatcher.writeFullPacket(packet, n, c.remote)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Conn) Close() error {
	return c.dispatcher.close(c)
}

// safeCloseRead close read chan in safe mode
func (c *Conn) safeCloseRead() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.isClosed {
		return io.ErrClosedPipe
	}
	c.isClosed = true
	close(c.read)
	return nil
}

func (c *Conn) LocalAddr() net.Addr {
	return c.dispatcher.localAddr()
}

func (c *Conn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *Conn) SetDeadline(t time.Time) error {
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

func (c *Conn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *Conn) sendAckByRead() error {
	ack, ackBits := c.recv.GetAck()
	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]
	header := packetHeader{
		packetKind: packetAck,
		ack:        ack,
		ackBits:    ackBits,
	}
	n := header.writeTo(packet)
	return c.dispatcher.writeFullPacket(packet, n, c.remote)
}

func (c *Conn) sendAckBySelectAck() error {
	ack, ackBits := c.recv.GetAck()
	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]
	header := packetHeader{
		packetKind: packetAck,
		ack:        ack,
		ackBits:    ackBits,
	}
	n := header.writeTo(packet)
	return c.dispatcher.writeFullPacket(packet, n, c.remote)
}

func (c *Conn) sendDialAck() error {
	packBuf := getBuffer()
	defer putBuffer(packBuf)
	packet := packBuf[:]
	n := packetHeader{packetKind: packetDialAck}.writeTo(packet)
	return c.dispatcher.writeFullPacket(packet, n, c.remote)
}
