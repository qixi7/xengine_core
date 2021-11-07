package xnet

import (
	"math"
	"reflect"
	"sync"
)

const (
	minPacketSize = 11 // ConnID(4) + seq(4) + MsgID(2) + Flag(1)
	CliHeadLen = 4 // BE4ByteHeader
	minCliBodyLen = minPacketSize
	MinCliMsgLen = CliHeadLen + minCliBodyLen
)

var (
	pbFact *packetFactory
)

func init() {
	pbFact = createFact()
}

type protoCreator struct {
	sliceT reflect.Type
	slice  reflect.Value
	idx    int
	mutex  sync.Mutex
}

func newProtoCreator(ele reflect.Type) *protoCreator {
	sliceT := reflect.SliceOf(ele.Elem())
	return &protoCreator{
		sliceT: sliceT,
		slice:  reflect.MakeSlice(sliceT, 0, 0),
		idx:    0,
	}
}

func (cr *protoCreator) syncNew() ProtoMessage {
	cr.mutex.Lock()
	if cr.idx >= cr.slice.Len() {
		cr.mutex.Unlock()
		slice := reflect.MakeSlice(cr.sliceT, 1024, 1024) // the bigger the slice, the more memory needed
		cr.mutex.Lock()
		if cr.idx >= cr.slice.Len() {
			cr.slice = slice
			cr.idx = 0
		}
	}
	ele := cr.slice.Index(cr.idx).Addr()
	cr.idx++
	cr.mutex.Unlock()
	return ele.Interface().(ProtoMessage)
}

type packSyncPool struct {
	proto   reflect.Type
	creator *protoCreator
}

type packetFactory struct {
	msgProto []*packSyncPool
	creator  SyncCreator_PBPacket
}

func createFact() *packetFactory {
	return &packetFactory{
		msgProto: make([]*packSyncPool, math.MaxUint16),
	}
}

func (pack *PBPacket) size() int {
	if pack.Body != nil {
		return minPacketSize + pack.Body.Size()
	}
	return minPacketSize
}

func (fact *packetFactory) register(id uint16, proto ProtoMessage) {
	pool := &packSyncPool{proto: reflect.TypeOf(proto)}
	pool.creator = newProtoCreator(pool.proto)
	fact.msgProto[id] = pool
}

func (fact *packetFactory) createByMsgID(id uint16) *PBPacket {
	pool := fact.msgProto[id]
	if pool == nil {
		return nil
	}
	pack := fact.creator.Get()
	pack.MsgID = id
	pack.Body = pool.creator.syncNew()
	return pack
}

func (fact *packetFactory) freePacket(pack *PBPacket) {
	pack.Body = nil
	fact.creator.Put(pack)
}

func RegisterPBMsgID(id uint16, proto ProtoMessage) {
	pbFact.register(id, proto)
}
