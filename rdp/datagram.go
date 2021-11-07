package rdp

import "encoding/binary"

const (
	packetData    byte = iota // 0
	packetAck                 // 1
	packetDial                // 2
	packetDialAck             // 3
)

type packetComponent interface {
	readFrom(packet []byte) int
	writeTo(packet []byte) int
}

var (
	_ = packetComponent(&packetHeader{})
	_ = packetComponent(&packetLoad{})
)

type packetHeader struct {
	packetKind byte
	ack        uint32
	ackBits    uint32
}

func (h *packetHeader) readFrom(packet []byte) int {
	if len(packet) < 2 {
		return 0
	}
	// 校验协议头
	if protocolID != packet[0] {
		return 0
	}
	h.packetKind = packet[1]
	size := 2
	switch h.packetKind {
	case packetAck:
		if len(packet) < 10 {
			return 0
		}
		h.ack = binary.BigEndian.Uint32(packet[2:6])
		h.ackBits = binary.BigEndian.Uint32(packet[6:10])
		size += 8
	}
	return size
}

func (h packetHeader) writeTo(packet []byte) int {
	if len(packet) < 2 {
		return 0
	}
	packet[0] = protocolID
	packet[1] = h.packetKind
	size := 2
	switch h.packetKind {
	case packetAck:
		if len(packet) < 10 {
			return 0
		}
		binary.BigEndian.PutUint32(packet[2:6], h.ack)
		binary.BigEndian.PutUint32(packet[6:10], h.ackBits)
		size += 8
	}
	return size
}

type packetLoad struct {
	seq  uint32
	size int
	buf  *buffer

	// non-serialize
	timestamp int64
}

// 4 for seq, 2 for size
func (l packetLoad) overhead() int {
	return 6
}

func (l packetLoad) writeTo(b []byte) int {
	if len(b) < l.size+l.overhead() {
		return 0
	}
	binary.BigEndian.PutUint32(b[:4], l.seq)
	binary.BigEndian.PutUint16(b[4:6], uint16(l.size))
	return l.overhead() + copy(b[l.overhead():], l.slice())
}

func (l *packetLoad) readFrom(b []byte) int {
	if len(b) < l.overhead() {
		return 0
	}
	l.seq = binary.BigEndian.Uint32(b[:4])
	l.size = int(binary.BigEndian.Uint16(b[4:6]))
	if len(b)-l.overhead() < l.size || len(l.buf[:]) < l.size {
		return 0
	}
	copy(l.slice(), b[l.overhead():])
	return l.overhead() + l.size
}

func (l *packetLoad) slice() []byte {
	return l.buf[:l.size]
}

func (l *packetLoad) free() {
	putBuffer(l.buf)
	l.buf = nil
}
