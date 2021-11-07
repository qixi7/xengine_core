package rdp

import (
	"sync"
)

type packet struct {
	known bool
	load  packetLoad
}

type queue struct {
	mu         sync.Mutex
	packets    []packet
	seq        uint32
	size       uint32
	delAck     uint32
	delAckBits uint32
}

type sendQueue interface {
	Clear(seq uint32)
	Get(offset uint32) (packetLoad, bool)
	Write(load packetLoad) bool
	Ack(ack, ackBits uint32, w *Window) int
}

type recvQueue interface {
	Set(load packetLoad) bool
	Read() (packetLoad, bool)
	GetAck() (uint32, uint32)
}

func newQueue(size int) *queue {
	return &queue{
		packets: make([]packet, size, size),
		size:    uint32(size),
	}
}

func (q *queue) Set(load packetLoad) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 本轮(size)存在未读完的packet. 只能放弃. load.seq超前.
	if load.seq-q.seq >= q.size {
		return false
	}

	index := load.seq % q.size
	p := &q.packets[index]

	// 重复
	if p.known {
		return false
	}
	*p = packet{
		known: true,
		load:  load,
	}

	return true
}

// read q.seq包
func (q *queue) Read() (packetLoad, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	index := q.seq % q.size
	p := &q.packets[index]

	if !p.known || p.load.seq != q.seq {
		return packetLoad{}, false
	}

	p.known = false
	load := p.load
	p.load = packetLoad{}

	q.seq++

	return load, true
}

func (q *queue) GetAck() (uint32, uint32) {
	q.mu.Lock()
	defer q.mu.Unlock()

	max := uint32(32)
	if q.size < max {
		max = q.size
	}

	ackBits := uint32(0)

	for offset := uint32(0); offset < max; offset++ {
		seq := q.seq + 1 + offset
		index := seq % q.size

		p := &q.packets[index]

		if p.known && p.load.seq == seq {
			// ackBits:
			// 		[bit32, ..., bit3,  bit2,  bit1]
			// 		[seq+n, ..., seq+3, seq+2, seq+1]
			ackBits |= 1 << offset
		}
	}

	return q.seq, ackBits
}

func (q *queue) Clear(seq uint32) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.clear(seq, nil)
}

func (q *queue) clear(seq uint32, w *Window) bool {
	index := seq % q.size
	p := &q.packets[index]

	if !p.known || p.load.seq != seq {
		return false
	}

	p.known = false
	if w != nil {
		w.Append(now() - p.load.timestamp)
	}
	p.load.free()
	return true
}

func (q *queue) Get(offset uint32) (packetLoad, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	//seq := q.seq - 1 + offset
	seq := q.seq + offset
	index := seq % q.size

	p := &q.packets[index]

	if !p.known || p.load.seq != seq {
		return packetLoad{}, false
	}

	return p.load, true
}

// 把load放进队列
func (q *queue) Write(load packetLoad) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	index := load.seq % q.size
	p := &q.packets[index]

	if p.known {
		if p.load.seq != load.seq {
			// 有别的包已经存在
			return false
		}
		// 已经存在. 不需要去释放, 也可以考虑释放后覆盖重写. 即 free()后执行下面的写代码
		return true
		//p.load.free()
	}

	*p = packet{
		known: true,
		load:  load,
	}
	return true
}

func (q *queue) Ack(ack, ackBits uint32, w *Window) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 比较缓存的最新ack和ackBits
	if q.delAck == ack && q.delAckBits == ackBits {
		return 0
	}

	n := 0
	if ack-q.seq <= q.size {
		// clear 到 ack 即 UNA(此编号前所有包已收到)
		for ; q.seq < ack; q.seq++ {
			if q.clear(q.seq, w) {
				n++
			}
		}

		// select ack
		for offset := uint32(1); ackBits != 0; offset++ {
			if ackBits&1 != 0 {
				if q.clear(ack+offset, w) {
					n++
				}
			}
			ackBits >>= 1
		}
	}

	q.delAck = ack
	q.delAckBits = ackBits

	return n
}
