package xnet

import (
	"github.com/pkg/errors"
	"net"
	"sync"
	"sync/atomic"
)

var (
	// ErrSessionNumLimit
	ErrSessionNumLimit = errors.New("session number max limit")
)

// 客户端连接管理类
type SessionMgr struct {
	sessionMap      sync.Map
	sessionNum      uint32
	sessionTotalNum uint64
	maxSessionNum   uint32
}

// create
func (m *SessionMgr) CreateSession(conn net.Conn, stream Streamer, cfg *sessionConfig) (*Session, error) {
	if atomic.LoadUint32(&m.sessionNum) >= m.maxSessionNum {
		return nil, ErrSessionNumLimit
	}
	atomic.AddUint32(&m.sessionNum, 1)
	atomic.AddUint64(&m.sessionTotalNum, 1)
	s := newSession(conn, stream, cfg)
	m.sessionMap.Store(s.ID(), s)
	return s, nil
}

func (m *SessionMgr) GetSession(id uint32) *Session {
	if s, ok := m.sessionMap.Load(id); ok {
		return s.(*Session)
	}
	return nil
}

func (m *SessionMgr) DeleteSession(c Sessioner) {
	atomic.AddUint32(&m.sessionNum, ^uint32(0))
	m.sessionMap.Delete(c.ID())
}
