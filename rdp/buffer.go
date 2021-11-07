package rdp

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
