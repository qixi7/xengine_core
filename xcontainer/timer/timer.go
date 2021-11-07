package timer

import (
	"container/heap"
	"time"
	"xcore/xmodule"
)

type priorityQueue struct {
	q   []*Item
	now time.Time
}

func (q *priorityQueue) Len() int {
	return len(q.q)
}

func (q *priorityQueue) Swap(i, j int) {
	q.q[i], q.q[j] = q.q[j], q.q[i]
	q.q[i].index = i
	q.q[j].index = j
}

func (q *priorityQueue) Push(x interface{}) {
	n := len(q.q)
	item := x.(*Item)
	item.index = n
	q.q = append(q.q, item)
}

func (q *priorityQueue) Pop() interface{} {
	old := q.q
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	q.q = old[0 : n-1]
	return item
}

func (q *priorityQueue) update(item *Item, dual time.Duration) {
	item.dual = dual
	item.since = q.now
	heap.Fix(q, item.index)
}

func (q *priorityQueue) Less(i, j int) bool {
	a := q.q[i].since.Add(q.q[i].dual)
	b := q.q[j].since.Add(q.q[j].dual)
	return a.Before(b)
}

type Item struct {
	index int
	dual  time.Duration
	since time.Time
	v     Timeout
}

func (Item) isTypeCreator() {}

type Timeout interface {
	Invoke(ctl *Controller, item *Item)
}

type Controller struct {
	q priorityQueue
}

func New() *Controller {
	c := &Controller{
		q: priorityQueue{
			q:   make([]*Item, 0),
			now: time.Now(),
		},
	}
	heap.Init(&c.q)
	return c
}

func (c *Controller) Tick() {
	c.q.now = time.Now()
	for {
		if c.q.Len() == 0 {
			break
		}
		item := heap.Pop(&c.q).(*Item)
		a := item.since.Add(item.dual)
		if a.After(c.q.now) {
			heap.Push(&c.q, item)
			break
		}
		item.v.Invoke(c, item)
	}
}

func (c *Controller) Add(dual time.Duration, v Timeout) *Item {
	item := New_Item()
	item.dual = dual
	item.since = c.q.now
	item.v = v
	heap.Push(&c.q, item)
	return item
}

func (c *Controller) AddItem(item *Item) {
	item.since = c.q.now
	heap.Push(&c.q, item)
}

func (c *Controller) Remove(item *Item) Timeout {
	if item.index != -1 {
		v, ok := heap.Remove(&c.q, item.index).(Timeout)
		if !ok {
			return nil
		}
		return v
	}
	return nil
}

// 修改item的时间
func (c *Controller) Update(item *Item, dual time.Duration) {
	if item.index != -1 {
		c.q.update(item, dual)
	}
}

func (c *Controller) Next(item *Item) {
	if item.index == -1 {
		item.since = item.since.Add(item.dual)
		heap.Push(&c.q, item)
	}
}

func (c *Controller) RemainTime(item *Item) time.Duration {
	return item.since.Add(item.dual).Sub(c.q.now)
}

func (c *Controller) Count() int {
	return len(c.q.q)
}

func (c *Controller) Init(selfGetter xmodule.DModuleGetter) bool {
	return true
}

func (c *Controller) Run(delta int64) {
	c.Tick()
}

func (c *Controller) Destroy() {
}

type Metric struct {
	TimeCount int
}

func (m *Metric) Pull(ctrl *Controller) {
	if ctrl != nil {
		m.TimeCount = len(ctrl.q.q)
	} else {
		m.TimeCount = -1
	}
}

func (m *Metric) Push() {
}
