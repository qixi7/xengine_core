package rdp

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type dispatcher interface {
	read()
	lastError() error
	close(*Conn) error
	localAddr() net.Addr
	ChecksumConn
}

var (
	_ dispatcher = (*serverDispatcher)(nil)
	_ dispatcher = (*clientDispatcher)(nil)
)

type serverDispatcher struct {
	ChecksumConn
	lastErr  error
	b        []byte
	listener *Listener
	clients  sync.Map
	closed   bool
}

func (d *serverDispatcher) lastError() error {
	return d.lastErr
}

func (d *serverDispatcher) timeout(t time.Duration) {
	now := time.Now()
	d.clients.Range(func(addr, conn interface{}) bool {
		if now.Sub(conn.(*Conn).lastSeen) > t {
			d.close(conn.(*Conn))
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
				RdpDebugLog("error", "readOnce net.Error err=%v", err)
				break
			}
			RdpDebugLog("error", "readOnce normal err=%v", err)
		}
	}

	d.closed = true
	close(d.listener.accept)
	d.clients.Range(func(addr, conn interface{}) bool {
		d.close(conn.(*Conn))
		return true
	})
}

func (d *serverDispatcher) readOnce() error {
	// read one pack to d.b([]byte)
	n, addr, err := d.readPacket(d.b)
	if err != nil {
		return err
	}

	// dispatch packets
	if conn, ok := d.getConn(addr); ok {
		header := packetHeader{}
		body := header.readFrom(d.b[:n])
		if body == 0 {
			RdpDebugLog("error", "readOnce readFrom %v:%v err, body==0.",
				addr.IP.String(), addr.Port)
			return nil
		}
		if header.packetKind == packetDial {
			RdpDebugLog("debug", "readOnce getConn dials from=%v:%v.",
				addr.IP.String(), addr.Port)
		}
		dispatchToConn(conn, header, d.b[body:n])
		return nil
	}

	// new dials
	RdpDebugLog("debug", "readOnce new dials from=%v:%v.",
		addr.IP.String(), addr.Port)
	header := packetHeader{}
	if header.readFrom(d.b[:n]) == 0 {
		return nil
	}
	if header.packetKind != packetDial {
		return nil
	}
	RdpDebugLog("debug", "readOnce put conn from=%v:%v. in channel.",
		addr.IP.String(), addr.Port)
	d.listener.accept <- dialPacket{addr}
	return nil
}

func dispatchToConn(conn *Conn, header packetHeader, b []byte) {
	conn.closeMu.Lock()
	defer conn.closeMu.Unlock()

	if conn.isClosed {
		return
	}
	buf := getBuffer()
	n := copy(buf[:], b)
	select {
	case conn.read <- incomingPacket{header, buf, n}:
	default:
		// 主动丢失.等重传
		putBuffer(buf)
		atomic.AddInt64(&metrics.InitiativeDrop, 1)
	}
}

func (d *serverDispatcher) getConn(addr *net.UDPAddr) (*Conn, bool) {
	if conn, ok := d.clients.Load(rawAddr(addr)); ok {
		return conn.(*Conn), true
	}
	return nil, false
}

func (d *serverDispatcher) setConn(addr *net.UDPAddr, conn *Conn) bool {
	if d.closed {
		return false
	}
	atomic.AddInt64(&metrics.ConnectionNum, 1)
	RdpDebugLog("debug", "setConn. addr=%v:%v", addr.IP.String(), addr.Port)
	d.clients.Store(rawAddr(addr), conn)
	return true
}

func (d *serverDispatcher) deleteConn(conn *Conn) {
	atomic.AddInt64(&metrics.ConnectionNum, -1)
	RdpDebugLog("debug", "deleteConn. addr=%v:%v", conn.remote.IP.String(), conn.remote.Port)
	d.clients.Delete(rawAddr(conn.remote))
}

func (d *serverDispatcher) close(conn *Conn) error {
	if err := conn.safeCloseRead(); err != nil {
		return err
	}
	d.deleteConn(conn)
	return nil
}

func (d *serverDispatcher) localAddr() net.Addr {
	return d.LocalAddr()
}

type clientDispatcher struct {
	ChecksumConn
	lastErr error
	client  *Conn
	b       []byte
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

func (d *clientDispatcher) readOnce() error {
	n, addr, err := d.readPacket(d.b)
	if err != nil {
		return err
	}
	if !addrEquals(addr, d.client.remote) {
		return nil
	}
	header := packetHeader{}
	body := header.readFrom(d.b[:n])
	if body == 0 {
		return nil
	}
	dispatchToConn(d.client, header, d.b[body:n])
	return nil
}

func (d *clientDispatcher) close(conn *Conn) error {
	return d.Close()
}

func (d *clientDispatcher) localAddr() net.Addr {
	return d.LocalAddr()
}
