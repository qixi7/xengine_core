package xlog

import (
	"errors"
	"github.com/qixi7/xengine_core/xlog/lumberjack-2.0"
	"sync"
	"sync/atomic"
	"time"
)

var (
	blockCount uint64
	logCount   uint64
	logBytes   uint64
	//_          io.WriteCloser = (*Logger)(nil)
)

type Logger struct {
	inputQ chan []byte
	closeQ chan int
	flushQ chan int
	wg     sync.WaitGroup
	l      *lumberjack.Logger
}

func (l *Logger) Write(p []byte) (n int, err error) {
	slice := make([]byte, len(p))
	copy(slice, p)
	select {
	case l.inputQ <- slice:
		return len(slice), nil
	default:
		atomic.AddUint64(&blockCount, 1)
		return 0, errors.New("log chan Full")
	}
}

func (l *Logger) Close() error {
	l.wg.Add(1)
	close(l.closeQ)
	l.wg.Wait()
	l.l = nil
	return nil
}

func (l *Logger) Sync() error {
	l.flushQ <- 1
	return nil
}

func NewLogger(fname string, msize, mage, mbackups, qsize, flushInterval int) *Logger {
	realLogger := &lumberjack.Logger{
		Filename:   fname,
		MaxSize:    msize,
		MaxAge:     mage,
		MaxBackups: mbackups,
	}

	l := &Logger{
		inputQ: make(chan []byte, qsize),
		closeQ: make(chan int),
		flushQ: make(chan int),
		l:      realLogger,
	}

	go func() {
		ticker := time.NewTicker(time.Second * time.Duration(flushInterval))
		defer ticker.Stop()
		for {
			select {
			case p := <-l.inputQ:
				atomic.AddUint64(&logCount, 1)
				atomic.AddUint64(&logBytes, uint64(len(p)))
				realLogger.Write(p)
			case <-ticker.C:
				// 刷新
			case <-l.flushQ:
				// 刷新
			case <-l.closeQ:
			out:
				for {
					select {
					case p := <-l.inputQ:
						realLogger.Write(p)
					default:
						break out
					}
				}
				realLogger.Close()
				l.wg.Done()
				return
			}
		}
	}()

	return l
}

func BlockCount() uint64 {
	return atomic.LoadUint64(&blockCount)
}

func LogCount() uint64 {
	return atomic.LoadUint64(&logCount)
}

func LogBytes() uint64 {
	return atomic.LoadUint64(&logBytes)
}
