package xrpc

import (
	"container/heap"
	"encoding/binary"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"xcore/xhlink"
	"xcore/xlog"
	"xcore/xmetric"
	"xcore/xmodule"
	"xcore/xnet"
)

const RpcTypeResponse = 0 // rpc 回应
const RpcTypeRequest = 1  // rpc 请求
const RpcProtocol = 1     // rpc 协议代号

// 注册的rpc函数
type methodType struct {
	method   reflect.Method
	argType  reflect.Type
	numCalls uint
}

// rpc结构体对应的所有rpc函数
type service struct {
	name   string                 // name of service
	rcvr   reflect.Value          // receiver of methods for the service
	typ    reflect.Type           // type of receiver
	method map[string]*methodType // registered methods
}

// rpc 管理类
type RPCStatic struct {
	cfg            *ClusterCfg
	srv            map[string]*service
	links          map[int32]*xnet.Link  // remoteID -> Link
	name2link      map[string]*xnet.Link // name -> Link
	timeoutTicker  *time.Ticker
	handler        map[string]ClusterHandler // key->nodeName, value->handler
	rpcDataMGetter xmodule.DModuleGetter
	selfGetter     xmodule.DModuleGetter // use when we need to transverse *RPCStatic
	metric         Metric
}

func NewRPCStatic(getter xmodule.DModuleGetter) *RPCStatic {
	rs := &RPCStatic{
		srv:            map[string]*service{},
		links:          map[int32]*xnet.Link{},
		name2link:      map[string]*xnet.Link{},
		timeoutTicker:  nil,
		handler:        map[string]ClusterHandler{},
		rpcDataMGetter: getter,
	}
	return rs
}

// rpc link
type rpcHLink struct {
	rpcWatcher   // rpc 行为监听
	rpcFormatter // rpc 编码解码器
}

type rpcWatcher struct {
	rs *RPCStatic
}

func (w *rpcWatcher) OnOpen(link *xnet.Link) {
	// 加入到static的map
	w.rs.links[link.GetRemoteID()] = link

	nodenames := strings.Split(link.GetRemoteName(), ",")
	for i := 0; i < len(nodenames); i++ {
		w.rs.name2link[nodenames[i]] = link
	}
	xlog.InfoF("%s[%s - %d] rpc open", link.GetRemoteName(), link.GetRemoteAddr(), link.GetRemoteID())
	w.rs.callClusterOnOpen(link.GetRemoteName(), link)
}

func (w *rpcWatcher) OnClose(link *xnet.Link) {
	if l, ok := w.rs.links[link.GetRemoteID()]; !ok {
		xlog.Errorf("rpc close, but not found remoteID %d", link.GetRemoteID())
		return
	} else if l != link {
		xlog.Errorf("rpc close, but not current link %d", link.GetRemoteID())
		return
	}
	delete(w.rs.links, link.GetRemoteID())
	xlog.InfoF("%s[%s - %d] rpc close", link.GetRemoteName(), link.GetRemoteAddr(), link.GetRemoteID())
	nodename := link.GetRemoteName()
	nodenames := strings.Split(nodename, ",")
	for i := 0; i < len(nodenames); i++ {
		delete(w.rs.name2link, nodenames[i])
		xlog.InfoF("rpc close [%s] name2link=%v", nodenames[i], w.rs.name2link)
	}
	w.rs.callClusterOnClose(nodename, link)
}

func (w *rpcWatcher) OnMessage(link *xnet.Link, pk xnet.SafePacket) {
	switch pk := pk.(type) {
	case request:
		srv, mtype, err := w.rs.getServiceMethod(pk.srvName)
		if err != nil {
			res := response{
				srvName: pk.srvName,
				seq:     pk.seq,
				err:     err.Error(),
			}
			link.Send(res)
			return
		}
		if mtype.argType != reflect.TypeOf(pk.arg) {
			res := response{
				srvName: pk.srvName,
				seq:     pk.seq,
				err:     strings.Join([]string{"arg type ", reflect.TypeOf(pk.arg).String(), "!=", mtype.argType.String()}, ""),
			}
			link.Send(res)
			return
		}
		var pipe Pipe
		pipe = &PipeImpl{rsgetter: w.rs.selfGetter, remoteID: link.GetRemoteID(), srvName: pk.srvName, seq: pk.seq}
		mtype.numCalls++
		mtype.method.Func.Call([]reflect.Value{srv.rcvr, reflect.ValueOf(pipe), reflect.ValueOf(pk.arg)})
		w.rs.metric.RequestInvokeCount++
	case response:
		data := w.rs.rpcDataMGetter.Get().(*RPCDynamicData)
		if call, ok := data.pending[pk.seq]; ok {
			delete(data.pending, pk.seq)
			if call.item != nil && call.item.index != -1 {
				heap.Remove(&data.timeout, call.item.index)
			}
			if pk.err != "" {
				xlog.Errorf("rpcWatcher OnMessage err=%v", pk.err)
				w.rs.metric.ErrorCount++
			} else {
				call.cb.Call(pk.reply, false)
				w.rs.metric.ResponseCount++
				w.rs.metric.RequestBytes += uint64(pk.reply.Size())
			}
		}
	default:
		xlog.Errorf("OnMessage unexpected type", pk)
	}
}

// new rpc link
func (rs *RPCStatic) newRPCHLink(addr string) *xhlink.HLinkUnitConfig {
	hlink := &rpcHLink{
		rpcWatcher: rpcWatcher{
			rs: rs,
		},
	}
	return &xhlink.HLinkUnitConfig{
		Addr:   addr,
		Linker: hlink,
	}
}

func (rs *RPCStatic) getServiceMethod(srvName string) (srv *service, mtype *methodType, err error) {
	var ok bool
	dot := strings.LastIndex(srvName, ".")
	if dot < 0 {
		err = errors.New("1 no service" + srvName)
		return
	}
	header := srvName[:dot]
	methodName := srvName[dot+1:]
	srv, ok = rs.srv[header]
	if !ok {
		err = errors.New("2 no service" + srvName)
		return
	}
	mtype, ok = srv.method[methodName]
	if !ok {
		err = errors.New("3 no service" + srvName)
		return
	}
	return
}

// Is this an exported - upper case - name?
func isExported(name string) bool {
	runeChar, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(runeChar)
}

func isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well
	return isExported(t.Name()) || t.PkgPath() == ""
}

// Register published in server the set of methods of the
// receiver value that satisfy the following conditions:
// - exported method of exported type
// - two arguments, both of exported type
// - one return value, of type error
// It returns an error if the receiver is not an exported type or has
// no suitable methods. It also logs the error using package log.
// The client accesses each method using a string of the form "Type.Method",
// where Type is the receiver's concrete type
func (rs *RPCStatic) Register(rcvr interface{}) error {
	return rs.register(rcvr, "", false)
}

// RegisterName is like Register but uses the provided name for the type
// instead of the receiver's concrete type
func (rs *RPCStatic) RegisterName(name string, rcvr interface{}) error {
	return rs.register(rcvr, name, false)
}

func (rs *RPCStatic) register(rcvr interface{}, name string, useName bool) error {
	s := new(service)
	s.typ = reflect.TypeOf(rcvr)
	s.rcvr = reflect.ValueOf(rcvr)
	sname := reflect.Indirect(s.rcvr).Type().Name()
	if useName {
		sname = name
	}
	if sname == "" {
		s := "rpc.Register: no service name for type " + s.typ.String()
		xlog.Errorf("RPCStatic register err=%v", s)
		return errors.New(s)
	}
	if !isExported(sname) && !useName {
		s := "rpc.Register: type " + sname + " is not exported"
		xlog.Errorf("RPCStatic register err=%v", s)
		return errors.New(s)
	}
	if _, present := rs.srv[name]; present {
		return errors.New("rpc: service already defined: " + sname)
	}
	s.name = sname

	// Install the methods
	s.method = suitableMethods(s.typ, true)

	if len(s.method) == 0 {
		str := ""

		// To help the user, see if a pointer receiver would work
		method := suitableMethods(reflect.PtrTo(s.typ), true)
		if len(method) == 0 {
			str = "rpc.Register: type " + sname + " has no exported methods of suitable type (hint: pass a pointer to value of that type)"
		} else {
			str = "rpc.Register: type " + sname + " has no exported methods of suitable type"
		}
		xlog.Errorf(str)
		return errors.New(str)
	}
	rs.srv[s.name] = s
	return nil
}

// suitableMethods returns suitable RPC methods of typ, it will report
// error using log if reportErr is true.
func suitableMethods(typ reflect.Type, reportErr bool) map[string]*methodType {
	methods := make(map[string]*methodType)
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mtype := method.Type
		mname := method.Name
		// Method must be exported
		if method.PkgPath != "" {
			continue
		}
		// Method needs three ins: receiver, Pipe, *args
		if mtype.NumIn() != 3 {
			if reportErr {
				xlog.Errorf("method: %s has wrong number of ins:%v", mname, mtype.NumIn())
			}
			continue
		}
		pipeType := mtype.In(1)
		if pipeType != reflect.TypeOf((*Pipe)(nil)).Elem() {
			if reportErr {
				xlog.Errorf("method %s the first argument must be rpc.Pipe", mname, pipeType)
			}
			continue
		}
		argType := mtype.In(2)
		if argType.Kind() != reflect.Ptr {
			if reportErr {
				xlog.Errorf("method %s argument type not a pointer", mname, argType)
			}
			continue
		}
		// argument type must be exported.
		if !isExportedOrBuiltinType(argType) {
			if reportErr {
				xlog.Errorf("method %s argument not exported", mname, argType)
			}
			continue
		}
		if !argType.Implements(reflect.TypeOf((*xnet.ProtoMessage)(nil)).Elem()) {
			if reportErr {
				xlog.Errorf("method %s argument has wrong number of outs", mname, mtype.NumOut())
			}
			continue
		}
		methods[mname] = &methodType{method: method, argType: argType}
	}
	return methods
}

// rpc 请求
type request struct {
	srvName string
	seq     uint64
	arg     xnet.ProtoMessage
}

// rpc 回应
type response struct {
	srvName string
	seq     uint64
	err     string
	reply   xnet.ProtoMessage
}

// send rpc call
func (rs *RPCStatic) sendCall(link *xnet.Link, srvName string, arg xnet.ProtoMessage, cb Callback) {
	data := rs.rpcDataMGetter.Get().(*RPCDynamicData)
	data.seq++
	seq := data.seq
	if cb != nil {
		item := &timeItem{since: time.Now(), seq: seq, name: srvName}
		data.pending[seq] = rpcCall{srvName: srvName, arg: arg, cb: cb, item: item}
		heap.Push(&data.timeout, item)
	}
	req := request{
		srvName: srvName,
		seq:     seq,
		arg:     arg,
	}
	link.Send(req)
	rs.metric.RequestCount++
	rs.metric.RequestBytes += uint64(arg.Size())
}

// 通过link节点名call
func (rs *RPCStatic) CallName(nodename, srvName string, arg xnet.ProtoMessage, cb Callback) bool {
	if link, ok := rs.name2link[nodename]; ok {
		rs.sendCall(link, srvName, arg, cb)
		return true
	}
	// rpc call 失败, 直接报错
	rs.metric.ErrorCount++

	_, ok := rs.name2link[nodename]
	if ok {
		xlog.Errorf("CallName disconnect nodename %s, method=%s", nodename, srvName)
	} else {
		xlog.Errorf("CallName no nodename %s, method=%s", nodename, srvName)
	}
	return false
}

// 通过link ID call
func (rs *RPCStatic) CallID(remoteID int32, srvName string, arg xnet.ProtoMessage, cb Callback) bool {
	if link, ok := rs.links[remoteID]; ok {
		rs.sendCall(link, srvName, arg, cb)
		return true
	}
	// rpc call 失败, 直接报错
	rs.metric.ErrorCount++
	_, ok := rs.links[remoteID]
	if ok {
		xlog.Errorf("CallName disconnect remoteID %d, srvName=%s", remoteID, srvName)
	} else {
		xlog.Errorf("CallName no remoteID %d, srvName=%s", remoteID, srvName)
	}
	return false
}

func (rs *RPCStatic) Init(selfGetter xmodule.DModuleGetter) bool {
	rs.selfGetter = selfGetter
	rs.timeoutTicker = time.NewTicker(time.Second)
	return true
}

func (rs *RPCStatic) Run(delta int64) {
	select {
	case <-rs.timeoutTicker.C:
		data := rs.rpcDataMGetter.Get().(*RPCDynamicData)
		for data.timeout.Len() > 0 {
			item := heap.Pop(&data.timeout).(*timeItem)
			if item.since.Add(time.Second * 60).After(time.Now()) {
				// not timeout
				if _, ok := data.pending[item.seq]; ok {
					heap.Push(&data.timeout, item)
					break
				}
			} else {
				// timeout
				if info, ok := data.pending[item.seq]; ok {
					delete(data.pending, item.seq)
					xlog.Errorf("rpc call timeout seq=%v, name=%v, info=%s", item.seq, item.name, info.srvName)
					// call.cb.Timeout()
					rs.metric.TimeoutCount++
					if info.item != nil {
						info.cb.Call(nil, true)
					}
				}
			}
		}
	default:
	}
}

func (rs *RPCStatic) Destroy() {
	rs.timeoutTicker.Stop()
}

func nameSize(name string) int {
	// 1 byte for name size
	return 1 + len(name)
}

func nameWrite(buf []byte, name string) int {
	// 1 byte for name size
	buf[0] = byte(len(name))
	name = name[:buf[0]]
	return 1 + copy(buf[1:], name)
}

func nameRead(buf []byte) (string, int) {
	length := int(buf[0])
	return string(buf[1 : 1+length]), 1 + length
}

type pbFormatter struct {
	pb   xnet.ProtoMessage
	size int
	name string
}

func (f *pbFormatter) Size() int {
	if f.pb != nil {
		f.name = proto.MessageName(f.pb)
		f.size = f.pb.Size()
		return 1 + nameSize(f.name) + 8 + f.size // 这里的8, 对应64位的size
	}
	return 1
}

func (f *pbFormatter) Write(buf []byte) int {
	if f.pb != nil {
		buf[0] = RpcProtocol
		pos := 1
		pos += nameWrite(buf[pos:], f.name)
		binary.BigEndian.PutUint64(buf[pos:], uint64(f.size)) // 64位的size
		pos += 8
		n, err := f.pb.MarshalTo(buf[pos:])
		if err != nil {
			xlog.Errorf("pbFormatter Write err=%v", err)
			buf[0] = 0
			return 1
		}
		pos += n
		return pos
	}
	buf[0] = 0
	return 1
}

func pbRead(buf []byte) (xnet.ProtoMessage, int, bool) {
	pos := 0
	if buf[pos] == RpcProtocol {
		pos++
		name, n := nameRead(buf[pos:])
		pos += n
		size := int(binary.BigEndian.Uint64(buf[pos:]))
		pos += 8
		pbType := proto.MessageType(name)
		if pbType == nil {
			panic("pbRead:" + name)
			return nil, pos + size, false
		}
		pbValue := reflect.New(pbType.Elem())
		pb := pbValue.Interface().(xnet.ProtoMessage)
		if err := pb.Unmarshal(buf[pos : pos+size]); err != nil {
			xlog.Errorf("pbRead Unmarshal err=%v", err)
			return nil, pos + size, false
		}
		pos += size
		return pb, pos, true
	}
	return nil, 1, true
}

// rpc 编码解码器
type rpcFormatter struct {
}

func (f *rpcFormatter) New() xnet.PacketFormater {
	return f
}

// rpc 协议序:
//	请求request: [rpc类型(1字节) + 函数名size(1字节) + 函数名(n1) + seq(8字节) + body(n1)]
//  回复response:[rpc类型(1字节) + 函数名size(1字节) + 函数名(n1) + seq(8字节) + err size(1字节) + err信息(n1) + body(n1)]
func (f *rpcFormatter) WritetoBuffer(pk xnet.SafePacket, link *xnet.Link) {
	switch pkType := pk.(type) {
	case response:
		pb := pbFormatter{pb: pkType.reply}
		buf := link.WritePacket(1 +
			nameSize(pkType.srvName) +
			8 +
			nameSize(pkType.err) +
			pb.Size())
		buf[0] = RpcTypeResponse
		size := 1
		size += nameWrite(buf[size:], pkType.srvName)
		binary.BigEndian.PutUint64(buf[size:], pkType.seq)
		size += 8
		size += nameWrite(buf[size:], pkType.err)
		size += pb.Write(buf[size:])
	case request:
		pb := pbFormatter{pb: pkType.arg}
		buf := link.WritePacket(1 +
			nameSize(pkType.srvName) +
			8 +
			pb.Size())
		buf[0] = RpcTypeRequest
		size := 1
		size += nameWrite(buf[size:], pkType.srvName)
		binary.BigEndian.PutUint64(buf[size:], pkType.seq)
		size += 8
		size += pb.Write(buf[size:])
	default:
		xlog.Errorf("write rpc type err.", pk)
	}
}

func (f *rpcFormatter) ReadfromBuffer(buf []byte) xnet.SafePacket {
	switch buf[0] {
	case RpcTypeResponse: // response
		var sz int
		var ok bool
		res := response{}
		size := 1
		res.srvName, sz = nameRead(buf[size:])
		size += sz
		res.seq = binary.BigEndian.Uint64(buf[size:])
		size += 8
		res.err, sz = nameRead(buf[size:])
		size += sz
		if res.reply, sz, ok = pbRead(buf[size:]); !ok {
			return nil
		}
		return res
	case RpcTypeRequest: // request
		var sz int
		var ok bool
		req := request{}
		size := 1
		req.srvName, sz = nameRead(buf[size:])
		size += sz
		req.seq = binary.BigEndian.Uint64(buf[size:])
		size += 8
		if req.arg, sz, ok = pbRead(buf[size:]); !ok {
			return nil
		}
		return req
	default:
		xlog.Errorf("read rpc type err.", buf[0])
		return nil
	}
}

func (f *rpcFormatter) DeprecateReadPacket(pk xnet.SafePacket) {
	// RPC 没有使用池化, 不需要回收
}

// --------------- 性能收集 ---------------

type Metric struct {
	LinkCount          int
	RequestCount       uint64
	RequestBytes       uint64
	RequestInvokeCount uint64
	ResponseCount      uint64
	ResponseBytes      uint64
	DroppedCount       uint64
	ErrorCount         uint64
	TimeoutCount       uint64
	// todo. if need. TopCalledService
}

func (m *Metric) Pull(rs *RPCStatic) {
	*m = rs.metric
	m.LinkCount = len(rs.links)
}

func (m *Metric) Push(gather *xmetric.Gather, ch chan<- prometheus.Metric) {
	gather.PushGaugeMetric(ch, "game_rpc_link_count", float64(m.LinkCount), nil)
	procNodeLabel := []string{"procnode"}
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.RequestCount), procNodeLabel, "request")
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.RequestInvokeCount), procNodeLabel, "invoke")
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.ResponseCount), procNodeLabel, "response")
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.DroppedCount), procNodeLabel, "dropped")
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.ErrorCount), procNodeLabel, "error")
	gather.PushCounterMetric(ch, "game_rpc_count", float64(m.TimeoutCount), procNodeLabel, "timeout")
	gather.PushCounterMetric(ch, "game_rpc_bytes", float64(m.RequestBytes), procNodeLabel, "request")
	gather.PushCounterMetric(ch, "game_rpc_bytes", float64(m.ResponseBytes), procNodeLabel, "response")
}
