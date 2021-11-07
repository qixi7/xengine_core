package xnet

import (
	"bufio"
	"io"
	"sync"
)

// gorouteine safe
type rdpWriter struct {
	bufWriter   *bufio.Writer
	maxSendSize int
	wMutex      sync.Mutex
}

// 对bufio.writer多包一层
func newRdpWriter(baseWriter io.Writer, size int) *rdpWriter {
	w := &rdpWriter{
		bufWriter: bufio.NewWriterSize(&datagramWriter{
			Base:        baseWriter,
			MaxSendSize: size,
		}, size),
		maxSendSize: size,
	}
	return w
}

func (w *rdpWriter) Write(p []byte) (int, error) {
	w.wMutex.Lock()
	defer w.wMutex.Unlock()
	n, err := w.bufWriter.Write(p)
	if err != nil {
		return 0, err
	}
	err = w.bufWriter.Flush()
	if err != nil {
		return 0, err
	}
	return n, nil
}

// datagramWriter is not thread safe
type datagramWriter struct {
	Base        io.Writer
	MaxSendSize int
}

func (w *datagramWriter) Write(p []byte) (int, error) {
	count := 0
	mss := w.MaxSendSize
	allBufDataLen := len(p)
	for allBufDataLen > mss {
		n, err := w.Base.Write(p[count : count+mss])
		count += n
		allBufDataLen -= n
		if err != nil {
			return count, err
		}
	}

	if allBufDataLen > 0 {
		n, err := w.Base.Write(p[count:])
		count += n
		if err != nil {
			return count, err
		}
	}
	return count, nil
}
