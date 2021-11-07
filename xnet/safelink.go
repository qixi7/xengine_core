package xnet

import (
	"net"
	"time"
)

/*
	This is the link between servers that does not discard packages
	with one logic thread
*/

const chanElemNum = 1024      // channel size
const bufferSize = 4096       // 接收、发送 buf 大小
const maxBufferSize = 1 << 20 // max buf size

type SafePacket interface{}

type PacketFormater interface {
	New() PacketFormater
	WritetoBuffer(cache []byte, pk SafePacket, link *Link, his *LinkHistory) []byte
	ReadfromBuffer(cache []byte, buf []byte) ([]byte, SafePacket)
	DeprecateReadPacket(pk SafePacket)
}

// (面向每个rpc连接)rpc 连接 handler
type Watcher interface {
	OnOpen(l *Link, udata interface{})
	OnReopen(l *Link, udata interface{})
	OnClose(l *Link) // maybe reopen after close
	OnMessage(l *Link, pk SafePacket)
}

// link 接收到的队列item
type queueElem struct {
	serial int64      // 序列号
	pk     SafePacket // pack
}

// link. 引擎层连接实例
type Link struct {
	conn          net.Conn                 // 底层连接实例(real connection)
	wat           Watcher                  // 提供给业务层的接口
	sendSig       chan struct{}            // send signal
	sendq         SingleConsumerSafePacket // send queue
	sendb         []byte                   // send buf
	recvq         chan queueElem           // recv queue
	pkfmt         PacketFormater           // packet format
	ackq          chan int64               // ack channel
	serial        int64                    // current serial number
	localID       int32                    // 本地路由ID
	localName     string                   // 本地连接名
	remoteID      int32                    // 远端路由ID
	remoteName    string                   // 远端连接名
	remoteTimeout time.Duration            // 远端超时时间
	isdial        bool                     // 是否主动dial过去的
	callClose     bool                     // 是否调用了close
	closed        bool                     // 是否已经关闭
	UData         interface{}              // user data interface
	RTT           time.Duration            // RTT
}

func newLink(conn net.Conn, wat Watcher, pkfmt PacketFormater, routeID int32, name string, isdial bool) *Link {
	return &Link{
		conn:       conn,
		wat:        wat,
		sendSig:    make(chan struct{}, 1),
		sendq:      SingleConsumerSafePacket{},
		sendb:      make([]byte, bufferSize),
		recvq:      make(chan queueElem, chanElemNum*16),
		pkfmt:      pkfmt.New(),
		ackq:       make(chan int64, 1),
		serial:     0,
		localID:    routeID,
		localName:  name,
		remoteName: "",
		isdial:     isdial,
		callClose:  false,
		closed:     false,
	}
}

// close
func (l *Link) Close() {
	l.callClose = true
	l.conn.Close()
}

// send pack
func (l *Link) Send(pk SafePacket) int64 {
	if l.closed {
		return l.serial
	}
	l.sendq.Push(pk)
	if l.sendq.Len() == 1 {
		select {
		case l.sendSig <- struct{}{}:
		default:
		}
	}
	return l.serial
}

func (l *Link) GetRemoteID() int32 {
	return l.remoteID
}

func (l *Link) GetRemoteName() string {
	return l.remoteName
}

func (l *Link) GetRemoteAddr() string {
	return l.conn.RemoteAddr().String()
}

func (l *Link) GetLocalAddr() string {
	return l.conn.LocalAddr().String()
}

// rpc 包序列: [bodySize(4字节) + flag(1字节) + seq(8位) + body(n)]
// 写入size
func (l *Link) WritePacket(bodySize int) (int64, []byte) {
	l.serial++
	if l.serial > linkSerialMax {
		l.serial = 0
	}
	serial := l.serial
	// 4 byte for bodySize
	l.sendb = append(l.sendb,
		byte(bodySize>>0),
		byte(bodySize>>8),
		byte(bodySize>>16),
		byte(bodySize>>24))
	// 1 byte for ack flag
	l.sendb = append(l.sendb, 0) // not ack
	// 8 byte for serial seq
	l.sendb = append(l.sendb,
		byte(serial>>0),
		byte(serial>>8),
		byte(serial>>16),
		byte(serial>>24),
		byte(serial>>32),
		byte(serial>>40),
		byte(serial>>48),
		byte(serial>>56))
	if len(l.sendb)+bodySize > cap(l.sendb) {
		slice := make([]byte, len(l.sendb), (len(l.sendb)+bodySize)*2)
		copy(slice, l.sendb)
		l.sendb = slice
	}
	bufWithoutHeader := l.sendb[len(l.sendb) : len(l.sendb)+bodySize]
	l.sendb = l.sendb[:len(l.sendb)+bodySize]
	return serial, bufWithoutHeader
}
