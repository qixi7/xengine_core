package xnet

import (
	"sync"
)

type ExpandQueueEvValue struct {
	buf   []evValue
	count int
	begin int
}

func (q *ExpandQueueEvValue) Push(elem evValue) {
	if q.count == len(q.buf) {
		q.resize((q.count + 1) * 2)
	}
	current := (q.begin + q.count) % len(q.buf)
	q.buf[current] = elem
	q.count++
}

func (q *ExpandQueueEvValue) Pop() evValue {
	if q.count == 0 {
		panic(q)
	}
	elem := q.buf[q.begin]
	var zero evValue
	q.buf[q.begin] = zero
	q.begin = (q.begin + 1) % len(q.buf)
	q.count--
	if len(q.buf) >= 65536 && q.count <= 1024 {
		q.resize(q.count * 2)
	}
	return elem
}

func (q *ExpandQueueEvValue) Len() int {
	return q.count
}

func (q *ExpandQueueEvValue) resize(size int) {
	slice := make([]evValue, q.count, size)
	count := copy(slice, q.buf[q.begin:])
	if count < q.count {
		copy(slice[count:], q.buf)
	}
	slice = slice[:size]
	q.buf = slice
	q.begin = 0
}

type ExpandQueueSafePacket struct {
	buf   []SafePacket
	count int
	begin int
}

func (q *ExpandQueueSafePacket) Push(elem SafePacket) {
	if q.count == len(q.buf) {
		q.resize((q.count + 1) * 2)
	}
	current := (q.begin + q.count) % len(q.buf)
	q.buf[current] = elem
	q.count++
}

func (q *ExpandQueueSafePacket) Pop() SafePacket {
	if q.count == 0 {
		panic(q)
	}
	elem := q.buf[q.begin]
	var zero SafePacket
	q.buf[q.begin] = zero
	q.begin = (q.begin + 1) % len(q.buf)
	q.count--
	if len(q.buf) >= 65536 && q.count <= 1024 {
		q.resize(q.count * 2)
	}
	return elem
}

func (q *ExpandQueueSafePacket) Len() int {
	return q.count
}

func (q *ExpandQueueSafePacket) resize(size int) {
	slice := make([]SafePacket, q.count, size)
	count := copy(slice, q.buf[q.begin:])
	if count < q.count {
		copy(slice[count:], q.buf)
	}
	slice = slice[:size]
	q.buf = slice
	q.begin = 0
}

type SingleConsumerSafePacket struct {
	q     [2]ExpandQueueSafePacket
	idx   int
	mutex sync.Mutex
}

func (uc *SingleConsumerSafePacket) Push(ele SafePacket) {
	uc.mutex.Lock()
	uc.q[uc.idx].Push(ele)
	uc.mutex.Unlock()
}

func (uc *SingleConsumerSafePacket) Pop(ele *SafePacket) bool {
	out := uc.idx ^ 1
	if uc.q[out].Len() != 0 {
		*ele = uc.q[out].Pop()
		return true
	}
	uc.mutex.Lock()
	if uc.q[uc.idx].Len() == 0 {
		uc.mutex.Unlock()
		return false
	}
	uc.idx = uc.idx ^ 1
	uc.mutex.Unlock()
	out = uc.idx ^ 1
	*ele = uc.q[out].Pop()
	return true
}

func (uc *SingleConsumerSafePacket) Len() int {
	return uc.q[0].Len() + uc.q[1].Len()
}
