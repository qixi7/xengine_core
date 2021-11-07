package xnet

import "net"

// stream interface. 对socket的封装.
type Streamer interface {
	Recv() ([]byte, *PBPacket, error, bool)
	Send(interface{}) error
	SendRaw([]byte) error
	Close() error
}

// stream 工厂 interface
type StreamFactory interface {
	NewStream(conn net.Conn) Streamer
	NewStreamWithDatagramConn(rw net.Conn, maxPackSize int) Streamer
}

// packet's head encode & decode
type HeadFormater interface {
	New() HeadFormater
	HeadLen() int
	Encode([]byte, int)
	Decode([]byte) int
}

type BodyFormater interface {
	New() BodyFormater
	ReadBufferAlloc(len int) []byte
	Encode(interface{}) ([]byte, error)
	Decode([]byte, Streamer) ([]byte, *PBPacket, error, bool)
}
