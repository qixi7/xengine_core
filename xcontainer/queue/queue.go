package queue

const initQueueLen = 16

type Queue struct {
	buf     []interface{}
	head    int
	tail    int
	count   int
	initLen int
}

func New() *Queue {
	return &Queue{
		buf:     make([]interface{}, initQueueLen),
		initLen: initQueueLen,
	}
}

func NewWithSize(size int) *Queue {
	return &Queue{
		buf:     make([]interface{}, size),
		initLen: size,
	}
}

func (q *Queue) resize() {
	newBuf := make([]interface{}, q.count<<1)
	if q.tail > q.head {
		copy(newBuf, q.buf[q.head:q.tail])
	} else {
		n := copy(newBuf, q.buf[q.head:])
		copy(newBuf[n:], q.buf[:q.tail])
	}
	q.head = 0
	q.tail = q.count
	q.buf = newBuf
}

func (q *Queue) Push(ele interface{}) {
	if q.count == len(q.buf) {
		q.resize()
	}
	q.buf[q.tail] = ele
	q.tail = (q.tail + 1) % len(q.buf)
	q.count++
}

func (q *Queue) Pop() interface{} {
	if q.count <= 0 {
		return nil
	}
	ret := q.buf[q.head]
	q.buf[q.head] = nil
	q.head = (q.head + 1) % len(q.buf)
	q.count--
	if len(q.buf) > q.initLen && (q.count<<2) == len(q.buf) {
		q.resize()
	}
	return ret
}

// (from q.head, 0 as first, -1 as last)
func (q *Queue) Get(i int) interface{} {
	if i < 0 {
		i += q.count
	}
	if i < 0 || i >= q.count {
		return nil
	}
	return q.buf[(q.head+i)&len(q.buf)]
}

// Peek return the ele at the head of the queue
func (q *Queue) Peek() interface{} {
	if q.count <= 0 {
		return nil
	}
	return q.buf[q.head]
}

func (q *Queue) Length() int {
	return q.count
}
