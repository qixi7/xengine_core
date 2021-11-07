package xrpc

import (
	"container/heap"
	"time"
	"xcore/xlog"
	"xcore/xmodule"
	"xcore/xnet"
)

type rpcLink struct {
	//disconn []rpcCall
}

// Call represents an active RPC
type rpcCall struct {
	srvName     string            // The name service and method to call
	arg         xnet.ProtoMessage // protobuf arg
	cb          Callback          // callback
	item        *timeItem         // callback 超时检测 item
	responseSeq uint64
}

// rpc data.
// 思路:
//	1、可以把对应的rpc连接发送失败的包缓存起来, 等重连上来后再补发.
// 	2、所以在RPCStatic维护一份rpc连接map, 在RPCDynamicData也维护了一份rpc连接map
//	两者不同之处是, 让一个rpc连接断开时RPCStatic会删除该连接, 而RPCDynamicData不会删除
//	不删除是为了当连接重连上来时, 判断是否有需要补发的rpc数据
// 	3、当然. 欢迎更好的做法 :)
type RPCDynamicData struct {
	links     map[int32]*rpcLink  // remoteID -> link
	name2link map[string]*rpcLink // nodename -> link
	pending   map[uint64]rpcCall  // 有callback的rpc请求会加入到pending, 收到callback时从pending中删除
	timeout   timeoutQueue        // 带callback的rpc请求超时队列
	seq       uint64              // 发送的rpc序列号
}

// new
func NewRPCDynamicData() *RPCDynamicData {
	data := &RPCDynamicData{
		links:     map[int32]*rpcLink{},
		name2link: map[string]*rpcLink{},
		pending:   map[uint64]rpcCall{},
		timeout:   timeoutQueue{},
		seq:       0,
	}
	heap.Init(&data.timeout)
	return data
}

// rpc回调超时检测item
type timeItem struct {
	since time.Time // 超时时间
	seq   uint64    // seq
	index int       // 堆中索引
	name  string    // rpc 函数名
}

// rpc回调超时检测队列
type timeoutQueue []*timeItem

// impl the heap interface
func (q timeoutQueue) Len() int {
	return len(q)
}

func (q timeoutQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index = i
	q[j].index = j
}

func (q *timeoutQueue) Push(x interface{}) {
	n := len(*q)
	item := x.(*timeItem)
	item.index = n
	*q = append(*q, item)
}

func (q *timeoutQueue) Pop() interface{} {
	old := *q
	n := len(*q)
	item := old[n-1]
	item.index = -1 // for safety
	*q = old[0 : n-1]
	return item
}

func (q timeoutQueue) Less(i, j int) bool {
	return q[i].since.Before(q[j].since)
}

// Pipe is a remote call reference with can be replied
type Pipe interface {
	Reply(reply xnet.ProtoMessage)
	RemoteID() int32
	Equal(other Pipe) bool
	Call(srvName string, arg xnet.ProtoMessage, cb Callback)
}

// 提供给rpc调用的对端pipe参数, 方便rpc调用函数去操作源link
type PipeImpl struct {
	rsgetter xmodule.DModuleGetter // Pipe will be in dynamic data and can not take *RPCStatic directly
	remoteID int32
	srvName  string
	seq      uint64
}

func (p *PipeImpl) Reply(reply xnet.ProtoMessage) {
	rs := p.rsgetter.Get().(*RPCStatic)
	if link, ok := rs.links[p.remoteID]; ok {
		res := response{
			srvName: p.srvName,
			seq:     p.seq,
			err:     "",
			reply:   reply,
		}
		link.Send(res)
		return
	}
	// rpc reply 失败, 直接报错
	rs.metric.ErrorCount++
	data := rs.rpcDataMGetter.Get().(*RPCDynamicData)
	_, ok := data.links[p.remoteID]
	if ok {
		xlog.Errorf("Pipe.Reply disconnect remoteID=%d, srvName=%s", p.remoteID, p.srvName)
	} else {
		xlog.Errorf("Pipe.Reply no remoteID=%d, srvName=%s", p.remoteID, p.srvName)
	}
	//if len(ldata.disconn) >= disconnectNum {
	//	rs.metric.DroppedCount++
	//	xlog.Errorf("Pipe.Reply overflow, remoteID=%d, len(disconn)=%d",
	//		p.remoteID, len(ldata.disconn))
	//	return
	//}
	//ldata.disconn = append(ldata.disconn, rpcCall{srvName: p.srvName, arg: reply, cb: nil, responseSeq: p.seq})
}

func (p *PipeImpl) RemoteID() int32 {
	return p.remoteID
}

func (p *PipeImpl) Equal(other Pipe) bool {
	p2 := other.(*PipeImpl)
	return p.remoteID == p2.remoteID && p.seq == p2.seq
}

func (p *PipeImpl) Call(srvName string, arg xnet.ProtoMessage, cb Callback) {
	rs := p.rsgetter.Get().(*RPCStatic)
	if link, ok := rs.links[p.remoteID]; ok {
		rs.sendCall(link, srvName, arg, cb)
		return
	}
	rs.metric.ErrorCount++
	data := rs.rpcDataMGetter.Get().(*RPCDynamicData)
	_, ok := data.links[p.remoteID]
	if ok {
		xlog.Errorf("Pipe.Call disconnect remoteID=%d, srvName=%s", p.remoteID, p.srvName)
	} else {
		xlog.Errorf("Pipe.Call no remoteID=%d, srvName=%s", p.remoteID, p.srvName)
	}
	return
	//if len(ldata.disconn) >= disconnectNum {
	//	rs.metric.DroppedCount++
	//	xlog.Errorf("Pipe.Call overflow, remoteID=%d, len(disconn)=%d",
	//		p.remoteID, len(ldata.disconn))
	//	return
	//}
	//ldata.disconn = append(ldata.disconn, rpcCall{srvName: p.srvName, arg: arg, cb: cb})
}

type Callback interface {
	Call(reply xnet.ProtoMessage, timeout bool)
}

func (d *RPCDynamicData) Init(selfGetter xmodule.DModuleGetter) bool {
	return true
}

func (d *RPCDynamicData) Run(delta int64) {
}

func (d *RPCDynamicData) Destroy() {
}
