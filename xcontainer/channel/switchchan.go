package channel

import (
	"sync"
)

// spsc channel

type SwitchChan struct {
	read, write []interface{}
	readPos     int
	mu          sync.RWMutex
	nonEmpty    *sync.Cond
	closed      bool
}

func (s *SwitchChan) Init(bufLen int) {
	s.read = make([]interface{}, 0, bufLen)
	s.write = make([]interface{}, 0, bufLen)
	s.nonEmpty = sync.NewCond(&s.mu)
}

func (s *SwitchChan) Write(ele interface{}) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return
	}
	s.write = append(s.write, ele)
	s.mu.RUnlock()
	s.nonEmpty.Signal()
}

func (s *SwitchChan) TryRead() (interface{}, bool) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, false
	}
	if len(s.read) > s.readPos {
		e := s.read[s.readPos]
		s.read[s.readPos] = nil
		s.readPos++
		s.mu.RUnlock()
		return e, true
	}
	s.mu.RUnlock()
	s.mu.Lock()
	if len(s.write) < 1 {
		s.mu.Unlock()
		return nil, false
	}
	s.readPos = 0
	s.read, s.write = s.write, s.read[:0]
	e := s.read[s.readPos]
	s.readPos++
	s.mu.Unlock()
	return e, true
}

func (s *SwitchChan) Read() (interface{}, bool) {
	for {
		s.mu.RLock()
		if s.closed {
			s.mu.RUnlock()
			return nil, false
		}
		if len(s.read) > s.readPos {
			e := s.read[s.readPos]
			s.read[s.readPos] = nil
			s.readPos++
			s.mu.RUnlock()
			return e, true
		}
		s.mu.RUnlock()
		s.mu.Lock()
		for len(s.write) < 1 && !s.closed {
			s.nonEmpty.Wait()
		}
		s.readPos = 0
		s.read, s.write = s.write, s.read[:0]
		s.mu.Unlock()
	}
}

func (s *SwitchChan) Len() int {
	s.mu.RLock()
	l := (len(s.read) - s.readPos) + len(s.write)
	s.mu.RUnlock()
	return l
}

func (s *SwitchChan) Close() {
	s.mu.Lock()
	s.closed = true
	s.read = nil
	s.write = nil
	s.mu.Unlock()
	s.nonEmpty.Broadcast()
}
