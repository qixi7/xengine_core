package xmemory

import "sync"

type ByteSliceCreator struct {
	buf []byte
	idx int
}

func (cr *ByteSliceCreator) Create(slen, scap, chunk int) []byte {
	if scap > chunk {
		return make([]byte, slen, scap)
	}
	if cr.idx+scap > len(cr.buf) {
		cr.buf = make([]byte, chunk, chunk)
		cr.idx = 0
	}
	current := cr.buf[cr.idx : cr.idx+slen : cr.idx+scap]
	cr.idx += scap
	return current
}

type ByteSliceSyncCreator struct {
	buf   []byte
	idx   int
	mutex sync.Mutex
}

func (cr *ByteSliceSyncCreator) Create(slen, scap, chunk int) []byte {
	if scap > chunk {
		return make([]byte, slen, scap)
	}
	cr.mutex.Lock()
	if cr.idx+scap > len(cr.buf) {
		cr.mutex.Unlock()
		newBuf := make([]byte, chunk, chunk)
		cr.mutex.Lock()
		if cr.idx+scap > len(cr.buf) {
			cr.buf = newBuf
			cr.idx = 0
		}
	}
	current := cr.buf[cr.idx : cr.idx+slen : cr.idx+scap]
	cr.idx += scap
	cr.mutex.Unlock()
	return current
}

type ByteSliceFixedCreator struct {
	fixed []byte
}

func (cr *ByteSliceFixedCreator) Create(slen, scap, chunk int) []byte {
	if scap > chunk {
		return make([]byte, slen, scap)
	}
	if cr.fixed == nil {
		cr.fixed = make([]byte, chunk)
	}

	return cr.fixed[:slen:scap]
}

type ByteSlicePtrCreator struct {
	buf [][]byte
	idx int
}

func (cr *ByteSlicePtrCreator) Create(chunk int) *[]byte {
	if cr.idx >= len(cr.buf) {
		cr.buf = make([][]byte, chunk, chunk)
		cr.idx = 0
	}
	current := &cr.buf[cr.idx]
	cr.idx++
	return current
}

type ByteSlicePtrSliceCreator struct {
	buf [][]byte
	idx int
}

func (cr *ByteSlicePtrSliceCreator) Create(slen, scap, chunk int) [][]byte {
	if scap > chunk {
		return make([][]byte, slen, scap)
	}
	if cr.idx+scap > len(cr.buf) {
		cr.buf = make([][]byte, chunk, chunk)
		cr.idx = 0
	}
	current := cr.buf[cr.idx : cr.idx+slen : cr.idx+scap]
	cr.idx += scap
	return current
}

type StringCreator struct {
	str []string
	idx int
}

func (cr *StringCreator) Create(slen, scap, chunk int) []string {
	if scap > chunk {
		return make([]string, slen, scap)
	}
	if cr.idx+scap > len(cr.str) {
		cr.str = make([]string, chunk, chunk)
		cr.idx = 0
	}
	current := cr.str[cr.idx : cr.idx+slen : cr.idx+scap]
	cr.idx += scap
	return current
}
