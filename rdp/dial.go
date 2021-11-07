package rdp

import (
	"context"
	"errors"
	"net"
	"time"
)

func Dial(network, address string) (net.Conn, error) {
	return DialContext(context.TODO(), network, address)
}

func DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return DialContext(ctx, network, address)
}

func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	remote, err := net.ResolveUDPAddr(network, address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP(network, nil)
	if err != nil {
		return nil, err
	}
	err = conn.SetReadBuffer(socketReadBufferSize)
	if err != nil {
		conn.Close()
		return nil, err
	}
	err = conn.SetWriteBuffer(socketWriteBufferSize)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		err = conn.SetDeadline(deadline)
		if err != nil {
			conn.Close()
			return nil, err
		}
		defer conn.SetDeadline(time.Time{})
	}

	d := &clientDispatcher{
		ChecksumConn: &crcConn{conn},
		b:            make([]byte, mtu, mtu),
	}

	done := make(chan struct{})

	go func() {
		packetBuf := getBuffer()
		defer putBuffer(packetBuf)
		packet := packetBuf[:]

		n := packetHeader{packetKind: packetDial}.writeTo(packet)
		for count := 0; ; count++ {
			d.writeFullPacket(packet, n, remote)
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	packetBuf := getBuffer()
	defer putBuffer(packetBuf)
	packet := packetBuf[:]

	for {
		select {
		case <-ctx.Done():
			return nil, errors.New("context is done")
		default:
		}
		n, addr, err := d.readPacket(packet)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if !addrEquals(addr, remote) {
			continue
		}
		header := packetHeader{}
		p := header.readFrom(packet[:n])
		if p == 0 || header.packetKind != packetDialAck {
			continue
		}
		break
	}

	close(done)

	d.client = &Conn{
		dispatcher: d,
		remote:     remote,
		read:       make(chan incomingPacket, incomingChanSize),
		send:       newQueue(queueSize),
		recv:       newQueue(queueSize),
		lastSeen:   time.Now(),
		readMu:     make(chan struct{}, 1),
	}

	go d.read()

	return d.client, nil
}
