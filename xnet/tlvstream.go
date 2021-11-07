package xnet

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
	"xcore/xlog"
)

const (
	packetSizeWarning = 64 * 1024       // 64 KB
	packetSizeLimit   = 4 * 1024 * 1024 // 4 MB
)

var (
	ErrPacketSize              = errors.New("pbcodec: packet size error")
	ErrBodyEncode              = errors.New("encode fail")
	ErrBodyEncoderNil          = errors.New("encoder nil")
	ErrBodyEncodeFail          = errors.New("ErrBodyEncodeFail")
	ErrPacketSizeLimitExceeded = errors.New("tlvstream: packet size limit exceeded")
)

type TLVStreamFactory struct {
	timeout    time.Duration
	headFormat HeadFormater
	bodyFormat BodyFormater
}

func NewTLVStreamFactory(timeout time.Duration, headFmt HeadFormater, bodyFmt BodyFormater) *TLVStreamFactory {
	return &TLVStreamFactory{
		timeout:    timeout,
		headFormat: headFmt,
		bodyFormat: bodyFmt,
	}
}

// 面向应用层stream. 封装了不同协议对应的stream. 这样做能将底层使用何种stream对上层透明
type TLVStream struct {
	rheadBuf   []byte            // read header buf
	writeBuf   []byte            // write header buf
	factory    *TLVStreamFactory // factory
	headLen    int               // 包头长度
	conn       net.Conn          // 连接实例
	timeout    time.Duration     // timeout
	headFormat HeadFormater      // 包头解码器
	bodyFormat BodyFormater      // 包体解码器

	// for datagram protocols
	reader io.Reader
	writer io.Writer
}

// new
func (f *TLVStreamFactory) NewStream(rw net.Conn) Streamer {
	headLen := f.headFormat.HeadLen()
	stream := &TLVStream{
		factory:    f,
		conn:       rw,
		reader:     rw,
		writer:     rw,
		headLen:    headLen,
		headFormat: f.headFormat.New(),
		bodyFormat: f.bodyFormat.New(),

		rheadBuf: make([]byte, headLen),
		writeBuf: make([]byte, headLen),

		timeout: f.timeout,
	}
	if f.timeout != 0 {
		_ = rw.SetDeadline(time.Now().Add(f.timeout))
	}
	return stream
}

// 针对rdp设计的data stream. 主要是因为udp包没有消息边界(一次读一个包, 而tcp有消息边界, 一次读一个指定len buf)
func (f *TLVStreamFactory) NewStreamWithDatagramConn(rw net.Conn, maxPacketSize int) Streamer {
	stream := f.NewStream(rw).(*TLVStream)
	stream.reader = bufio.NewReaderSize(stream.reader, maxPacketSize)
	stream.writer = newRdpWriter(stream.writer, maxPacketSize)
	return stream
}

// Recv not goroutine safe
func (c *TLVStream) Recv() ([]byte, *PBPacket, error, bool) {
	if c.timeout != 0 {
		if err := c.conn.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			errNetLog(err, fmt.Sprintf("TLVStream. SetReadDeadline readTimeout err=%v", err))
			return nil, nil, err, false
		}
	}
	var err error
	// 读取、解析包头
	if _, err = io.ReadFull(c.reader, c.rheadBuf); err != nil {
		errNetLog(err, fmt.Sprintf("TLVStream. io.ReadFull 1 err=%v", err))
		return nil, nil, err, false
	}
	bodyLen := c.headFormat.Decode(c.rheadBuf)
	totalLen := bodyLen + c.headLen
	if totalLen < c.headLen {
		return nil, nil, ErrPacketSize, false
	}
	if totalLen > packetSizeLimit {
		xlog.Errorf("over size limit packet: %v bytes", totalLen)
		return nil, nil, ErrPacketSizeLimitExceeded, false
	}

	if totalLen > packetSizeWarning {
		xlog.Warnf("huge packet: %v bytes", totalLen)
	}

	// alloc buf
	buf := c.bodyFormat.ReadBufferAlloc(totalLen)
	//buf := c.recvBuff[:totalLen]

	//copy(buf, c.rheadBuf)
	for i := range c.rheadBuf {
		buf[i] = c.rheadBuf[i]
	}
	// 读取、解析包体
	if _, err = io.ReadFull(c.reader, buf[c.headLen:totalLen]); err != nil {
		errNetLog(err, fmt.Sprintf("TLVStream. io.ReadFull 2 err=%v", err))
		return nil, nil, err, false
	}
	return c.bodyFormat.Decode(buf, c)
}

// Send not goroutine safe
func (c *TLVStream) Send(msg interface{}) error {
	// 编码包体
	buf, err := c.bodyFormat.Encode(msg)
	if err != nil {
		xlog.Errorf("TLVStream bodyFormat Encode err=%v", err)
		return err
	}
	if c.timeout != 0 {
		if err = c.conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
			errNetLog(err, fmt.Sprintf("TLVStream SetWriteDeadline err=%v", err))
			return err
		}
	}

	// 编码包头
	c.headFormat.Encode(c.writeBuf[:c.headLen], len(buf))
	c.writeBuf = append(c.writeBuf[:c.headLen], buf...)

	// send
	if _, err = c.writer.Write(c.writeBuf); err != nil {
		errNetLog(err, fmt.Sprintf("TLVStream writer Write 1 err=%v", err))
		return err
	}

	//if iobuf, ok := c.writer.(*bufio.Writer); ok {
	//	err = iobuf.Flush()
	//	if err != nil {
	//		errNetLog(err, fmt.Sprintf("TLVStream writer Write 2 err=%v", err))
	//	}
	//}

	return err
}

// SendRaw goroutine safe
func (c *TLVStream) SendRaw(msg []byte) error {
	if c.timeout != 0 {
		if err := c.conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
			return err
		}
	}
	_, err := c.writer.Write(msg)
	if err != nil {
		return err
	}

	//if buf, ok := c.writer.(*bufio.Writer); ok {
	//	err = buf.Flush()
	//}

	return err
}

func (c *TLVStream) Close() error {
	return c.conn.Close()
}
