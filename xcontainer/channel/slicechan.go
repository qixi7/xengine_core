package channel

import (
	"github.com/qixi7/xengine_core/xcontainer/queue"
	"sync"
)

// mpmc channel

type SliceChan struct {
	queue    *queue.Queue
	mu       sync.RWMutex
	nonEmpty *sync.Cond
	closed   bool
}

func (s *SliceChan) Init(bufLen int) {
	s.queue = queue.NewWithSize(bufLen)
	s.nonEmpty = sync.NewCond(&s.mu)
}

func (s *SliceChan) Write(ele interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		panic("write to a closed chan")
	}
	s.queue.Push(ele)
	s.nonEmpty.Signal()
}

func (s *SliceChan) TryRead() (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false
	}
	if s.queue.Length() < 1 {
		return nil, false
	}
	return s.queue.Pop(), true
}

func (s *SliceChan) Read() (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.queue.Length() < 1 && !s.closed {
		s.nonEmpty.Wait()
	}
	if s.closed {
		return nil, false
	}
	return s.queue.Pop(), true
}

func (s *SliceChan) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queue.Length()
}

func (s *SliceChan) Close() {
	s.mu.Lock()
	s.closed = true
	s.nonEmpty.Broadcast()
	s.mu.Unlock()
}
