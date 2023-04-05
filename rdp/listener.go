package rdpkit

import (
	"errors"
	"io"
	"net"
	"time"
)

var (
	ErrDialAck = errors.New("send back dialAck")
)

const network = "udp"

func Listen(address string) (*Listener, error) {
	addr, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP(network, addr)
	if err != nil {
		return nil, err
	}
	err = conn.SetReadBuffer(socketReadBufferSize)
	if err != nil {
		return nil, err
	}
	err = conn.SetWriteBuffer(socketWriteBufferSize)
	if err != nil {
		return nil, err
	}
	l := &Listener{
		accept:     make(chan dialPacket, incomingChanSize),
		dispatcher: newServerDispatcher(conn),
		conn:       conn,
		closeSig:   make(chan struct{}),
	}
	l.dispatcher.listener = l
	go l.dispatcher.read()
	go l.dispatcher.sender()
	return l, nil
}

type dialPacket struct {
	from *net.UDPAddr
}

type Listener struct {
	accept     chan dialPacket
	dispatcher *serverDispatcher
	conn       *net.UDPConn
	timeout    *time.Ticker
	closeSig   chan struct{}
}

func (l *Listener) SetClientTimeout(t time.Duration) {
	if l.timeout != nil {
		panic("SetClientTimeout can be called only once")
	}
	l.timeout = time.NewTicker(t)
	go func() {
		defer l.timeout.Stop()
		for {
			select {
			case <-l.timeout.C:
				l.dispatcher.timeout(t)
			case <-l.closeSig:
				return
			}
		}
	}()
}

func (l *Listener) Accept() (net.Conn, error) {
	d := l.dispatcher
	var p dialPacket
Read:
	for {
		var ok bool
		p, ok = <-l.accept
		if !ok {
			return nil, io.ErrClosedPipe
		}
		if _, ok := d.getConn(p.from); ok {
			RdpDebugLog("error", "p.from=%v:%v, is already in.",
				p.from.IP.String(), p.from.Port)
			continue
		}
		break Read
	}

	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]
	n := packetHeader{packetKind: packetDialAck}.writeTo(packet)
	RdpDebugLog("debug", "new conn from=%v:%v, come in.", p.from.IP.String(), p.from.Port)
	if err := d.writeDirect(packet, n, p.from); err != nil {
		errorF("writeBack DialAck err=%v", err)
		return nil, ErrDialAck
	}
	c := NewRdpConn(d, p.from, l.conn)
	if !d.setConn(p.from, c) {
		return nil, io.ErrClosedPipe
	}
	return c, nil
}

func (l *Listener) Close() error {
	close(l.closeSig)
	return l.conn.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
