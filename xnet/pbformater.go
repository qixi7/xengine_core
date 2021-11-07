package xnet

import (
	"encoding/binary"
	"fmt"
	"github.com/pkg/errors"
	"sync"
	"xcore/xlog"
	"xcore/xmemory"
	"xcore/xnet/codec"
)

const (
	bufSize           = 4096
	compressThreshold = 128              // 压缩阈值. buf超过该大小考虑压缩
	maxPBLoadSize     = 16 * 1024 * 1024 // 16MB
)

type decodeBuffer [maxPBLoadSize]byte

var decodeBuffPool = &sync.Pool{New: func() interface{} {
	var buf decodeBuffer
	return &buf
}}

var (
	ErrPBDecodeMinSize = errors.New("protobuf message min size")
	ErrPBMaxSize       = errors.New("protobuf message max size exceeded")
	ErrPBMsgType       = errors.New("should be *PBPacket")
)

const (
	UDPFlag = 1 << iota
	CompressFlag
)

// PBFormater connID(4)|seq(4)|msgID(2)|flag(1)|body|
// PBFormater protobuf formatter for client
type PBFormater struct {
	codec.Codec
	encoding  string
	headsize  int
	readBuf   xmemory.ByteSliceFixedCreator
	encodeBuf []byte
}

func NewPBFormater(headLen int, encoding string) *PBFormater {
	return &PBFormater{
		Codec:     codec.New(encoding),
		encoding:  encoding,
		headsize:  headLen,
		encodeBuf: make([]byte, bufferSize+minPacketSize),
	}
}

func (f *PBFormater) New() BodyFormater {
	return NewPBFormater(f.headsize, f.encoding)
}

func (f *PBFormater) ReadBufferAlloc(len int) []byte {
	return f.readBuf.Create(len, len, 4096)
}

func (f *PBFormater) Encode(msg interface{}) ([]byte, error) {
	m, ok := msg.(*PBPacket)
	if !ok {
		return nil, ErrPBMsgType
	}
	head := f.encodeBuf[:minPacketSize]
	binary.BigEndian.PutUint32(head[:4], m.ConnID)
	binary.BigEndian.PutUint32(head[4:8], m.Seq)
	binary.BigEndian.PutUint16(head[8:10], m.MsgID)
	head[10] = m.Flag
	if m.Body != nil {
		size := m.Body.Size()
		if size > maxPBLoadSize {
			return nil, ErrPBMaxSize
		}
		var body []byte
			body = make([]byte, size) // body should not escape
			_, err := m.Body.MarshalTo(body)
			if err != nil {
				return nil, err
			}

		// 是否需要压缩
		if f.Codec != nil && len(body) > compressThreshold {
			buf2 := make([]byte, len(body))
			compressed, ok := f.Codec.Encode(buf2, body)
			if ok {
				head[10] |= CompressFlag
				body = compressed
			}
		}
		totalSize := minPacketSize + len(body)
		if len(f.encodeBuf) < totalSize {
			buf := make([]byte, totalSize)
			copy(buf, f.encodeBuf[:minPacketSize])
			copy(buf[minPacketSize:], body)
			return buf, nil
		}
		copy(f.encodeBuf[minPacketSize:], body)
		return f.encodeBuf[:totalSize], nil
	}
	return head, nil
}

func (f *PBFormater) Decode(msg []byte, s Streamer) ([]byte, *PBPacket, error, bool) {
	if len(msg) < minPacketSize+f.headsize {
		xlog.Errorf("PBFormater Decode err, len(msg=%d), headsize=%d, msg=%v",
			len(msg), f.headsize, msg)
		return nil, nil, ErrPBDecodeMinSize, false
	}
	msg = (msg)[f.headsize:]
	connID := binary.BigEndian.Uint32((msg)[0:4])
	seq := binary.BigEndian.Uint32((msg)[4:8])
	msgID := binary.BigEndian.Uint16((msg)[8:10])
	flag := (msg)[10]
	body := (msg)[minPacketSize:]

	packet := pbFact.createByMsgID(msgID)
	if packet == nil {
		return nil, nil, errors.New(fmt.Sprintf("unregistered message id=%d", msgID)), false
	}

	// 是否有压缩需要解压
	var decBuf *decodeBuffer
	if flag&CompressFlag != 0 {
		if f.Codec == nil {
			return nil, nil, errors.New("codec error"), false
		}
		decBuf = decodeBuffPool.Get().(*decodeBuffer)
		data, err := f.Codec.Decode(decBuf[:], body)
		if err != nil {
			xlog.Errorf(fmt.Sprintf("Decode msgID=%d, err=%v", msgID, err))
			return nil, nil, err, false
		}
		body = data
	}

	// 调用protobuf的反序列化接口
	if err := packet.Body.Unmarshal(body); err != nil {
		return nil, nil, err, false
	}
	if decBuf != nil {
		decodeBuffPool.Put(decBuf)
	}

	packet.ConnID = connID
	packet.MsgID = msgID
	packet.Seq = seq
	packet.Flag = flag

	return nil, packet, nil, true
}
