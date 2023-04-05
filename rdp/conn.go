package rdpkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

const (
	defaultMaxSplitPacketSize = 500 // 单个Rdp包最大限制
	defaultRedundantNum       = 5   // 冗余包数
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

type RdpConn struct {
	read          *switchQ      // 将网络协程读到的数据有序地放到这里
	dispatcher    dispatcher    // 连接实例封装. 负责包收发,关闭
	remote        *net.UDPAddr  // UDP接收端地址
	conn          *net.UDPConn  // 真实UDP连接实例
	send          sendQueue     // 网络发送队列
	recv          recvQueue     // 网络接收队列
	seq           uint32        // 当前发包序列号seq
	readMu        chan struct{} // 读协程是否最近读到过数据
	lastSeen      time.Time     // 本连接最近一次收到包, 用于超时判断
	w             Window        // 收集RTT窗口(暂未使用)
	readDeadline  time.Time     // 读超时
	writeDeadline time.Time     // 写超时
	closeFlag     int32

	writeBuf []byte // for write
	flushBuf []byte // for flush
	// param
	maxSplitSize int    // 单包最大Size
	redundantNum uint32 // 冗余包数
	// for batch
	haveReadData int32 // 是否有读到最新需要的数据(发包时, 如果有读到最新的数据, 附带上ACK)
}

func NewRdpConn(d dispatcher, remote *net.UDPAddr, conn *net.UDPConn) *RdpConn {
	c := &RdpConn{
		dispatcher:   d,
		conn:         conn,
		remote:       remote,
		send:         newQueue(queueSize),
		recv:         newQueue(queueSize),
		lastSeen:     time.Now(),
		readMu:       make(chan struct{}, 1),
		writeBuf:     make([]byte, mtu),
		flushBuf:     make([]byte, mtu),
		maxSplitSize: defaultMaxSplitPacketSize,
		redundantNum: defaultRedundantNum,
	}
	// init read queue
	c.read = &switchQ{}
	c.read.Init(incomingChanSize)
	return c
}

func (c *RdpConn) Read(b []byte) (n int, err error) {
	c.readMu <- struct{}{}
	defer func() { <-c.readMu }()

	if !c.readDeadline.IsZero() {
		ctx, cancel := context.WithDeadline(context.Background(), c.readDeadline)
		defer cancel()
		return c.readContext(ctx, b)
	}
	return c.readContext(context.Background(), b)
}

func (c *RdpConn) readContext(ctx context.Context, b []byte) (int, error) {
	receiveAny := false
	readCount := 0

	for {
		if load, ok := c.recv.Read(); ok {
			subPacket := load.subPacket
			// 判断是否分包
			n := copy(b[readCount:], load.slice())
			readCount += n
			//_ = c.sendAckByRead()
			atomic.SwapInt32(&c.haveReadData, 1)
			load.free()
			c.lastSeen = time.Now()

			atomic.AddUint64(&metrics.RecvBytes, uint64(n))
			atomic.AddUint64(&metrics.RecvPackets, 1)

			if subPacket {
				continue
			}
			return readCount, nil
		} else if receiveAny {
			receiveAny = false
			_ = c.sendAckBySelectAck()
		}

		// 这里有超时机制
		for !receiveAny {
			p, err := c.read.Read(ctx)
			if err != nil {
				return 0, err
			}
			receiveAny = c.transferToQueue(p) >= 1 && p.header.packetKind == packetData
		}
	}
}

func (c *RdpConn) transferToQueue(p *incomingPacket) int {
	setSuccessNum := 0
	switch p.header.packetKind {
	case packetDial:
		_ = c.sendDialAck()
	case packetData:
		b := p.slice()
		load := packetLoad{buf: getBuffer()}
		//packStr := strings.Builder{}
		//packStr.WriteString("[")
	ReadLoop:
		for {
			n := load.readFrom(b)
			if n == 0 {
				load.free()
				break ReadLoop
			}
			//packStr.WriteString(fmt.Sprintf("%d, ", load.seq))
			// 有load Set失败. 那么也相当于receiveAny==false
			if c.recv.Set(load) {
				setSuccessNum++
				load.buf = getBuffer()
			}
			b = b[n:]
		}
		//packStr.WriteString("]")
		//infoF("recv recvSeq=%d, seq=%v", c.recv.GetSeq(), packStr.String())
		p.free()
	case packetAck:
		c.send.Ack(p.header.ack, p.header.ackBits, &c.w)
	}
	p.free()
	return setSuccessNum
}

func (c *RdpConn) Write(b []byte) (int, error) {
	if !c.writeDeadline.IsZero() {
		ctx, cancel := context.WithDeadline(context.Background(), c.writeDeadline)
		defer cancel()
		return c.writeContext(ctx, b)
	}
	return c.writeContext(context.Background(), b)
}

func (c *RdpConn) writeContext(ctx context.Context, b []byte) (int, error) {
	// 这里发包 & 分包
	count := 0
	allBufDataLen := len(b)
	mss := c.maxSplitSize
	for allBufDataLen > mss {
		// 判断是否子包
		n, err := c.writeOne(ctx, b[count:count+mss], allBufDataLen-mss > 0)
		count += n
		allBufDataLen -= n
		if err != nil {
			return count, err
		}
	}

	if allBufDataLen > 0 {
		n, err := c.writeOne(ctx, b[count:], false)
		count += n
		if err != nil {
			return count, err
		}
	}

	return count, nil
}

func (c *RdpConn) writeOne(ctx context.Context, b []byte, subPacket bool) (int, error) {
	// 组包
	n := packetHeader{packetKind: packetData}.writeTo(c.writeBuf)
	if len(b) > c.maxSplitSize {
		errorF("maxSplitPacketSize. len(b)=%d, b=%v", len(b), b)
		return 0, ErrMaxLoadSizeExceeded
	}
	loadBuf := getBuffer()
	load := packetLoad{
		seq:       c.seq,
		size:      copy(loadBuf[:], b),
		subPacket: subPacket,
		buf:       loadBuf,
	}

	//packStr := strings.Builder{}
	//packStr.WriteString(fmt.Sprintf("[%d", c.seq))
	// 写入当前包
	loadLen := load.writeTo(c.writeBuf[n:])
	n += loadLen
	// here: packetHeader + data

	nowUnixMs := nowMs()
	load.timestamp = nowUnixMs
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
			load.free()
			return 0, context.DeadlineExceeded
		case c.readMu <- struct{}{}:
			// 因为此时有可能也收到了数据包
			p, err := c.read.Read(ctx)
			if err != nil {
				<-c.readMu
				ticker.Stop()
				load.free()
				return 0, err
			}
			c.transferToQueue(p)
			<-c.readMu
		case <-ticker.C:
		}
	}
	if ticker != nil {
		ticker.Stop()
	}

	// minRTT := c.w.Min()
	for pack := uint32(0); pack < c.redundantNum; pack++ {
		p, ok := c.send.Get(pack)
		if !ok {
			continue
		}
		if load.seq == p.seq || !after(load.seq, p.seq) {
			// infoF("p.seq=%d not after load.seq=%d", p.seq, load.seq)
			break
		}
		if n+p.overhead()+p.size+c.dispatcher.Overhead() > mtu {
			//infoF("over mtu to add p.seq=%d", p.seq)
			break
		}
		// if (nowUnixMs - p.timestamp) < minRTT {
		//	infoF("timediff=%d, minRTT=%d", nowUnixMs-p.timestamp, minRTT)
		// 	continue
		// }

		n += p.writeTo(c.writeBuf[n:])
		//packStr.WriteString(fmt.Sprintf(", %d", p.seq))
	}
	//packStr.WriteString("]")

	// writeBuf to queue
	//bts := getBuffer()
	//copy(bts[:], c.writeBuf[:n])
	//if err := c.dispatcher.writeDirect(bts[:], n, c.remote); err != nil {
	if err := c.sendBatch(c.writeBuf, n, !subPacket); err != nil {
		errorF("conn write err=%v", err)
		return 0, err
	} else {
		//infoF("write minRTT=%d, sendAck=%d, seq=%v",
		//	minRTT, c.send.GetSeq(), packStr.String())
	}
	//putBuffer(bts)

	atomic.AddUint64(&metrics.SendBytes, uint64(len(b)))
	atomic.AddUint64(&metrics.SendPackets, 1)

	c.seq++

	return len(b), nil
}

func (c *RdpConn) sendBatch(buf []byte, size int, writeAck bool) error {
	if c.isClosed() {
		return errors.New(fmt.Sprintf("rdpConn already close, remoteAddr=%s", c.remote.String()))
	}
	if writeAck {
		// 有数据读到过, 发包的时候, 带上ack
		if atomic.SwapInt32(&c.haveReadData, 0) > 0 {
			_ = c.sendAckByRead()
		}
	}
	return c.dispatcher.batchFill(buf, size, c.remote)
}

// 该连接是否已经关闭
func (c *RdpConn) isClosed() bool {
	return atomic.LoadInt32(&c.closeFlag) > 0
}

// 标记关闭连接
func (c *RdpConn) closeConn() {
	atomic.AddInt32(&c.closeFlag, 1)
}

func (c *RdpConn) flush() error {
	atomic.AddInt64(&metrics.WriteBlockRetransmit, 1)

	n := packetHeader{packetKind: packetData}.writeTo(c.flushBuf)

	writtenNum := uint32(0)
	for offsetPack := uint32(0); offsetPack < queueSize; offsetPack++ {
		p, ok := c.send.Get(offsetPack)
		if !ok {
			continue
		}
		if !after(c.seq, p.seq) {
			break
		}
		if n+p.overhead()+p.size+c.dispatcher.Overhead() > mtu {
			break
		}
		n += p.writeTo(c.flushBuf[n:])
		writtenNum++
		if writtenNum >= c.redundantNum {
			break
		}
	}

	if writtenNum > 0 {
		err := c.sendBatch(c.flushBuf, n, false)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *RdpConn) Close() error {
	return c.dispatcher.close(c)
}

// safeCloseRead close read chan in safe mode
func (c *RdpConn) safeCloseRead() error {
	if c.isClosed() {
		return io.ErrClosedPipe
	}
	c.closeConn()
	// 所有需要put的, 得put
	c.send.CloseAndClear()
	c.recv.CloseAndClear()
	c.read.Close()
	return nil
}

func (c *RdpConn) LocalAddr() net.Addr {
	return c.dispatcher.localAddr()
}

func (c *RdpConn) RemoteAddr() net.Addr {
	return c.remote
}

func (c *RdpConn) SetDeadline(t time.Time) error {
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

func (c *RdpConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *RdpConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *RdpConn) SetMaxSplitSize(size int) {
	c.maxSplitSize = size
}

func (c *RdpConn) SetRedundantNum(redu uint32) {
	if redu < 0 || redu > 10 {
		// 大于10还是有点离谱..
		errorF("SetRedundantNum err, redu=%d", redu)
		return
	}
	c.redundantNum = redu
}

func (c *RdpConn) sendAckByRead() error {
	// 有read到数据的时候再Ack
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
	return c.sendBatch(packet, n, false)
}

func (c *RdpConn) sendAckBySelectAck() error {
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
	return c.sendBatch(packet, n, false)
}

func (c *RdpConn) sendDialAck() error {
	packBuf := getBuffer()
	defer putBuffer(packBuf)
	packet := packBuf[:]
	n := packetHeader{packetKind: packetDialAck}.writeTo(packet)
	return c.sendBatch(packet, n, false)
}
