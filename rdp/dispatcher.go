package rdpkit

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type dispatcher interface {
	lastError() error
	close(*RdpConn) error
	localAddr() net.Addr
	ChecksumConn
}

var (
	_ dispatcher = (*serverDispatcher)(nil)
	_ dispatcher = (*clientDispatcher)(nil)
)

// 作为rdp服务端使用
type serverDispatcher struct {
	ChecksumConn
	lastErr  error
	listener *Listener
	clients  sync.Map
	closed   bool
}

func newServerDispatcher(conn *net.UDPConn) *serverDispatcher {
	d := &serverDispatcher{}
	d.ChecksumConn = newCrcConn(conn, d.readLogic)
	return d
}

func (d *serverDispatcher) lastError() error {
	return d.lastErr
}

func (d *serverDispatcher) timeout(t time.Duration) {
	now := time.Now()
	d.clients.Range(func(addr, conn interface{}) bool {
		if now.Sub(conn.(*RdpConn).lastSeen) > t {
			d.close(conn.(*RdpConn))
		}
		return true
	})
}

func (d *serverDispatcher) read() {
	for {
		if err := d.readOnce(); err != nil {
			if netErr, ok := err.(net.Error); ok {
				if netErr.Timeout() {
					continue
				}
				d.lastErr = err
				if _, open := <-d.listener.closeSig; open {
					RdpDebugLog("error", "readOnce net.Error err=%v", err)
				}
				break
			}
			RdpDebugLog("error", "readOnce normal err=%v", err)
		}
	}

	d.closed = true
	close(d.listener.accept)
	d.clients.Range(func(addr, conn interface{}) bool {
		d.close(conn.(*RdpConn))
		return true
	})
}

func (d *serverDispatcher) sender() {
	sendTicker := time.NewTicker(time.Millisecond * 10)
	defer sendTicker.Stop()
	for {
		select {
		case <-sendTicker.C:
			if d.closed {
				return
			}
			// uncork
			if err := d.batchFlush(); err != nil {
				errorF("serverDispatcher batchFlush err=%v", err)
			}
		case <-d.listener.closeSig:
			return
		}
	}
}

func (d *serverDispatcher) readLogic(data []byte, addr *net.UDPAddr) {
	//infoF("readLogic once! len=%d, data=%v", len(data), data)
	n := len(data)
	header := packetHeader{}
	body := header.readFrom(data[:n])
	if body == 0 {
		RdpDebugLog("error", "readOnce readFrom %v:%v err, body==0.",
			addr.IP.String(), addr.Port)
		return
	}

	// dispatch packets to exist connection
	if conn, ok := d.getConn(addr); ok {
		if header.packetKind == packetDial {
			RdpDebugLog("debug", "readOnce getConn dials from=%v:%v.",
				addr.IP.String(), addr.Port)
		}
		dispatchToConn(conn, header, data[body:n])
		return
	}

	// new dials
	RdpDebugLog("debug", "readOnce new dials from=%v:%v.",
		addr.IP.String(), addr.Port)

	if header.packetKind != packetDial {
		return
	}

	// 新来的连接如果过多, 不应该阻塞accept队列
	if len(d.listener.accept) < cap(d.listener.accept) {
		RdpDebugLog("debug", "readOnce put conn from=%v:%v. in channel.",
			addr.IP.String(), addr.Port)
		d.listener.accept <- dialPacket{addr}
	}
}

func (d *serverDispatcher) readOnce() error {
	// 一次读多个
	return d.GetXConn().ReadBatch()
}

func dispatchToConn(conn *RdpConn, header packetHeader, b []byte) {
	if conn.isClosed() {
		return
	}
	buf := getBuffer()
	n := copy(buf[:], b)
	err := conn.read.Write(&incomingPacket{header, buf, n})
	if err != nil {
		errorF("dispatchToConn conn=%s, err=%v", conn.remote.String(), err)
		putBuffer(buf)
		return
	}
}

func (d *serverDispatcher) getConn(addr *net.UDPAddr) (*RdpConn, bool) {
	if conn, ok := d.clients.Load(rawAddr(addr)); ok {
		return conn.(*RdpConn), true
	}
	return nil, false
}

func (d *serverDispatcher) setConn(addr *net.UDPAddr, conn *RdpConn) bool {
	if d.closed {
		return false
	}
	atomic.AddInt64(&metrics.ConnectionNum, 1)
	RdpDebugLog("debug", "setConn. addr=%v:%v", addr.IP.String(), addr.Port)
	d.clients.Store(rawAddr(addr), conn)
	return true
}

func (d *serverDispatcher) deleteConn(conn *RdpConn) {
	atomic.AddInt64(&metrics.ConnectionNum, -1)
	RdpDebugLog("debug", "deleteConn. addr=%v:%v", conn.remote.IP.String(), conn.remote.Port)
	d.clients.Delete(rawAddr(conn.remote))
}

func (d *serverDispatcher) close(conn *RdpConn) error {
	if err := conn.safeCloseRead(); err != nil {
		return err
	}
	d.deleteConn(conn)
	return nil
}

func (d *serverDispatcher) localAddr() net.Addr {
	return d.LocalAddr()
}

// 作为rdp客户端使用
type clientDispatcher struct {
	ChecksumConn
	lastErr  error
	client   *RdpConn
	closeSig chan struct{}
}

func newClientDispatcher(conn *net.UDPConn) *clientDispatcher {
	d := &clientDispatcher{}
	d.closeSig = make(chan struct{})
	d.ChecksumConn = newCrcConn(conn, d.readLogic)
	return d
}

func (d *clientDispatcher) lastError() error {
	return d.lastErr
}

func (d *clientDispatcher) read() {
	defer d.client.safeCloseRead()
	for {
		if err := d.readOnce(); err != nil {
			if netErr, ok := err.(net.Error); ok {
				if netErr.Timeout() {
					continue
				}
				d.lastErr = err
				break
			}
		}
	}
}

func (d *clientDispatcher) sender() {
	sendTicker := time.NewTicker(time.Millisecond * 10)
	defer sendTicker.Stop()
	for {
		select {
		case <-sendTicker.C:
			// uncork
			if err := d.batchFlush(); err != nil {
				errorF("clientDispatcher batchFlush err=%v", err)
			}
		case <-d.closeSig:
			return
		}
	}
}

func (d *clientDispatcher) readLogic(data []byte, addr *net.UDPAddr) {
	n := len(data)
	if !addrEquals(addr, d.client.remote) {
		errorF("clientDispatcher remote addr is not match")
		return
	}
	header := packetHeader{}
	body := header.readFrom(data[:n])
	if body == 0 {
		errorF("clientDispatcher read no body")
		return
	}
	dispatchToConn(d.client, header, data[body:n])
}

func (d *clientDispatcher) readOnce() error {
	return d.GetXConn().ReadBatch()
}

func (d *clientDispatcher) close(conn *RdpConn) error {
	close(d.closeSig)
	return d.Close()
}

func (d *clientDispatcher) localAddr() net.Addr {
	return d.LocalAddr()
}
