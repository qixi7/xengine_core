package xnet

import "sync"

// google protobuf 生成代码会有这些函数定义
type ProtoMessage interface {
	Reset()
	String() string
	ProtoMessage()
	Size() int
	MarshalTo([]byte) (int, error)
	Unmarshal([]byte) error
}

type PBPacket struct {
	ConnID uint32
	Seq    uint32
	MsgID  uint16
	Flag   uint8
	Body   ProtoMessage
}

func (*PBPacket) isTypeCreator() {
}

type SyncCreator_PBPacket struct {
	pool sync.Pool
}

func (cr *SyncCreator_PBPacket) Get() *PBPacket {
	pk := cr.pool.Get()
	if pk == nil {
		return &PBPacket{}
	}
	pack := pk.(*PBPacket)
	*pack = PBPacket{} // reinitialize
	return pack
}

func (cr *SyncCreator_PBPacket) Put(pk *PBPacket) {
	cr.pool.Put(pk)
}
