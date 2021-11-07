package channel

type UnBlockChannel interface {
	Init(int)
	Write(interface{})
	TryRead() (interface{}, bool)
	Read() (interface{}, bool)
	Close()
	Len() int
}

// 单生产者单消费者
type UnBlockSPSC struct {
	SwitchChan
}

// 多生产者单消费者
type UnBlockMPSC struct {
	SliceChan
}

// 单生产者多消费者
type UnBlockSPMC struct {
	SliceChan
}

// 多生产者多消费者
type UnBlockMPMC struct {
	SliceChan
}
