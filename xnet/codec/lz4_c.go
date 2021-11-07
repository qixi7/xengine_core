package codec

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#define LZ4_STATIC_LINKING_ONLY
#include "lz4.h"
*/
import "C"
import (
	"fmt"
	"github.com/pkg/errors"
	"sync"
	"unsafe"
)

var (
	lz4cSizeofState   = int(C.LZ4_sizeofState())
	lz4cHashTablePool = &sync.Pool{
		New: func() interface{} {
			return make([]byte, lz4cSizeofState, lz4cSizeofState)
		},
	}
)

type LZ4C struct{}

func (l *LZ4C) Encode(dst, src []byte) ([]byte, bool) {
	state := lz4cHashTablePool.Get()
	defer lz4cHashTablePool.Put(state)
	b := unsafe.Pointer(&state.([]byte)[0])
	n := int(C.LZ4_compress_fast_extState_fastReset(
		b,
		(*C.char)(unsafe.Pointer(&src[0])),
		(*C.char)(unsafe.Pointer(&dst[0])),
		C.int(len(src)),
		C.int(len(dst)),
		0,
	))
	if n == 0 || n >= len(src) {
		return src, false
	}
	return dst[:n], true
}

func (l *LZ4C) Decode(dst, src []byte) ([]byte, error) {
	n := int(C.LZ4_decompress_safe(
		(*C.char)(unsafe.Pointer(&src[0])),
		(*C.char)(unsafe.Pointer(&dst[0])),
		C.int(len(src)),
		C.int(len(dst)),
	))
	if n <= 0 {
		return nil, errors.New(fmt.Sprintf("lz4 decode error:%v", n))
	}
	return dst[:n], nil
}
