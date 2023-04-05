package rdpkit

import (
	"context"
	"errors"
	"sync"
)

var (
	queueClosed = errors.New("queue closed")
)

// spsc channel

type switchQ struct {
	read, write []*incomingPacket
	readPos     int
	mu          sync.RWMutex
	chReadEvent chan struct{}
	closed      bool
}

func (s *switchQ) Init(bufLen int) {
	s.read = make([]*incomingPacket, 0, bufLen)
	s.write = make([]*incomingPacket, 0, bufLen)
	s.chReadEvent = make(chan struct{}, 1)
}

func (s *switchQ) Write(ele *incomingPacket) error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return queueClosed
	}
	s.write = append(s.write, ele)
	s.mu.RUnlock()
	s.notifyDataEvent()
	return nil
}

func (s *switchQ) Read(ctx context.Context) (*incomingPacket, error) {
	for {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return nil, queueClosed
		}
		if len(s.read) > s.readPos {
			e := s.read[s.readPos]
			s.read[s.readPos] = nil
			s.readPos++
			s.mu.RUnlock()
			return e, nil
		}
		s.mu.RUnlock()
		s.mu.Lock()
		if len(s.write) < 1 && !s.closed {
			s.mu.Unlock()
			// 等待有数据事件
			select {
			case <-ctx.Done():
				// 超时
				return nil, context.DeadlineExceeded
			case <-s.chReadEvent:
				if s.closed {
					return nil, queueClosed
				}
				// 有数据
				s.mu.Lock()
			}
		}
		s.readPos = 0
		s.read, s.write = s.write, s.read[:0]
		s.mu.Unlock()
	}
}

func (s *switchQ) Len() int {
	s.mu.RLock()
	l := (len(s.read) - s.readPos) + len(s.write)
	s.mu.RUnlock()
	return l
}

func (s *switchQ) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.notifyDataEvent()
	// 清理所有队列中的数据
	s.mu.Lock()
	if len(s.write) > 0 {
		for i := range s.write {
			if s.write[i] != nil {
				s.write[i].free()
			}
		}
	}
	if len(s.read) > 0 {
		for i := range s.read {
			if s.read[i] != nil {
				s.read[i].free()
			}
		}
	}
	s.read = nil
	s.write = nil
	s.mu.Unlock()
}

func (s *switchQ) notifyDataEvent() {
	select {
	case s.chReadEvent <- struct{}{}:
	default:
	}
}
