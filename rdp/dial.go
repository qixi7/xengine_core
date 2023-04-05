package rdpkit

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
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

	d := newClientDispatcher(conn)
	d.client = NewRdpConn(d, remote, conn)

	done := make(chan struct{})

	go func() {
		packetBuf := getBuffer()
		defer putBuffer(packetBuf)
		packet := packetBuf[:]

		n := packetHeader{packetKind: packetDial}.writeTo(packet)
		for count := 0; ; count++ {
			//infoF("send dial once!")
			if err := d.writeDirect(packet, n, remote); err != nil {
				errorF("dial err=%v", err)
			}
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
		// 阻塞读
		n, addr, err := conn.ReadFromUDP(packet)
		if err != nil {
			conn.Close()
			return nil, err
		}
		atomic.AddUint64(&metrics.UDPRecvBytes, uint64(n))
		atomic.AddUint64(&metrics.UDPRecvPackets, 1)
		n = checkPacket(packet, n)
		if n < 1 {
			err = errChecksumMismatch
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

	go d.read()
	go d.sender()

	return d.client, nil
}
