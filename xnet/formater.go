package xnet

import "encoding/binary"

var (
	BE4ByteHead BE4ByteHeader
)

// BE2ByteHeader big endian, four bytes head. for client with gateway
type BE4ByteHeader struct {
}

func NewBE4ByteHeader() *BE4ByteHeader {
	return &BE4ByteHead
}

func (header *BE4ByteHeader) New() HeadFormater {
	return &BE4ByteHead
}

func (header *BE4ByteHeader) HeadLen() int {
	return 4
}

func (header *BE4ByteHeader) Encode(h []byte, size int) {
	binary.BigEndian.PutUint32(h, uint32(size))
}

func (header *BE4ByteHeader) Decode(h []byte) int {
	return int(binary.BigEndian.Uint32(h))
}
