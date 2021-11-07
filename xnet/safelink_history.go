package xnet

import (
	"xcore/xmemory"
)

type backElem struct {
	serial int64
	data   []byte
}

type LinkHistory struct {
	localAck  int64 // finished receive packet
	maxSerial int64
	back      []backElem
	creator   xmemory.ByteSliceCreator

	remoteID   int32
	remoteName string
}

func newLinkHistory() *LinkHistory {
	return &LinkHistory{}
}

func (his *LinkHistory) Append(serial int64, buf []byte) {
	elem := backElem{serial: serial}
	elem.data = his.creator.Create(len(buf), len(buf), 1<<16)
	copy(elem.data, buf)
	his.back = append(his.back, elem)
}
