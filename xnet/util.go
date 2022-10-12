package xnet

import (
	"encoding/binary"
	"github.com/qixi7/xengine_core/xlog"
	"github.com/qixi7/xengine_core/xmemory"
	"io"
	"runtime"
	"strings"
	"time"
)

var byteSliceCreator = xmemory.ByteSliceCreator{}
var byteSliceSyncCreator = xmemory.ByteSliceSyncCreator{}

func writeFull(conn io.Writer, b []byte) error {
	size := len(b)
	pos := 0
	for pos < size {
		n, err := conn.Write(b[pos:])
		if err != nil {
			return err
		}
		pos += n
		if n == 0 {
			runtime.Gosched()
		}
	}
	return nil
}

func readFull(conn io.Reader, b []byte) error {
	size := len(b)
	pos := 0
	for pos < size {
		n, err := conn.Read(b[pos:])
		if err != nil {
			return err
		}
		pos += n
	}
	return nil
}

const (
	headSize  = 12 // [size(4字节) + seq(8位)]
	heartSize = 8
)

const (
	linkSerialHeart int64 = 0x7fffffffffffffff - iota
	linkSerialMax
)

func writeBuffer(conn io.Writer, serial int64, buf []byte) error {
	// 多线程环境
	head := byteSliceSyncCreator.Create(headSize, headSize, 4096)
	binary.LittleEndian.PutUint32(head[:], uint32(len(buf)))
	binary.LittleEndian.PutUint64(head[4:], uint64(serial))
	if err := writeFull(conn, head[:]); err != nil {
		return err
	}
	if len(buf) != 0 {
		if err := writeFull(conn, buf); err != nil {
			return err
		}
	}
	return nil
}

func readBuffer(conn io.Reader, buf []byte) (int64, []byte, error) {
	buf = buf[:headSize]
	if err := readFull(conn, buf); err != nil {
		return 0, buf, err
	}
	size := binary.LittleEndian.Uint32(buf[:4])
	serial := int64(binary.LittleEndian.Uint64(buf[4:]))
	if cap(buf) < int(size) {
		buf = make([]byte, size)
	} else {
		buf = buf[:size]
	}
	if len(buf) != 0 {
		if err := readFull(conn, buf); err != nil {
			return serial, buf, err
		}
	}
	return serial, buf, nil
}

func heartSerialize() []byte {
	var buf [heartSize]byte
	binary.LittleEndian.PutUint64(buf[:8], uint64(time.Now().UnixNano()))
	return buf[:]
}

func heartDeserialize(buf []byte) (rtt time.Duration) {
	if len(buf) < heartSize {
		return 0
	}
	lastTime := int64(binary.LittleEndian.Uint64(buf[:8]))
	rtt = time.Duration(time.Now().UnixNano() - lastTime)
	if rtt < 0 || rtt > 10*time.Minute {
		// 异常数据
		rtt = 0
	}
	return
}

// 检测是否需要打该log
func errNetLog(err error, str string) {
	errstr := err.Error()
	if !strings.Contains(errstr, "use of closed network connection") &&
		!strings.Contains(errstr, "EOF") &&
		//!strings.Contains(errstr, "timeout") &&
		!strings.Contains(errstr, "io: read/write on closed pipe") &&
		err != errSessionClosed &&
		!strings.Contains(errstr, "read: connection reset by peer") {
		xlog.ErrorfSkip(1, str)
	}
}
