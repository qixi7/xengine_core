package rdpkit

import "sync"

type buffer [mtu]byte

func newBuffer() interface{} {
	var buf buffer
	return &buf
}

var bufferPool = &sync.Pool{New: newBuffer}

func getBuffer() *buffer {
	return bufferPool.Get().(*buffer)
}

func putBuffer(buf *buffer) {
	bufferPool.Put(buf)
}

//var (
//	// a system-wide packet buffer shared among sending, receiving and FEC
//	// to mitigate high-frequency memory allocation for packets
//	xmitBuf sync.Pool
//)
//
//func init() {
//	xmitBuf.New = func() interface{} {
//		return make([]byte, mtu)
//	}
//}
