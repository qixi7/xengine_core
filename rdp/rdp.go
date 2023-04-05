package rdpkit

import (
	"encoding/binary"
	"errors"
	"net"
	"time"
)

/***
		RDP - Redundant Data Protocol
一、概述
	冗余传输协议。
	1).基于UDP. 算法实现
	2).通过冗余包实现可靠传输
	3).协议层摒弃定时器以更大提升性能, 依赖外部定时器
	4).更短的RTO. (TCP->指数增长, KCP->1.5倍增长, RDP弃用RTO)
	5).Ack+Select Ack更快确认机制
	6).支持链路探路探测控制冗余包粒度
	7).支持分包(传输大包, 但不推荐传输大包)

二、缺点
	1).为了更快到达对端, 没有实现拥塞控制, 因此使用在小包传输场景。比如帧同步

三、TODO.
	1).根据探测结果动态调整冗余个数

四、协议序
	ACK包: [协议头(1字节) + 包类型(1字节) + ack(4字节) + ackBit(4字节)]
	数据包: [协议头(1字节) + 包类型(1字节) + seq(4字节) + bodySize(2字节) + 是否子包(1字节) + body]
	其他包: [协议头(1字节) + 包类型(1字节)]
*/

var (
	ErrMTUExceeded         = errors.New("MTU exceeded")
	ErrMaxLoadSizeExceeded = errors.New("maxSplitPackeetSize exceeded")
)

const (
	// 不能变
	protocolID = byte(0x43)
	magic      = uint32(0x5A11ADFC)

	// 推荐值, 尽量别改
	queueSize             = 256             // 收发队列大小
	rttWindow             = 4               // 收集最近RTT个数
	incomingChanSize      = 256             // 监听端口收到包后转发给对应的conn实例的buf队列大小
	socketReadBufferSize  = 4 * 1024 * 1024 // 底层socket Read Buf大小
	socketWriteBufferSize = 4 * 1024 * 1024 // 底层socket Write Buf大小
	mtu                   = 1200            // 默认值

	// 可改
	OpenDebugLog = true // debug log switch
)

func rawAddr(addr *net.UDPAddr) (raw [6]byte) {
	copy(raw[:4], addr.IP[:4])
	binary.BigEndian.PutUint16(raw[4:], uint16(addr.Port))
	return
}

func addrEquals(addr1, addr2 *net.UDPAddr) bool {
	return (addr1 == addr2) || (addr1.Port == addr2.Port) && addr1.IP.Equal(addr2.IP)
}

// seq1 after seq2
func after(seq1, seq2 uint32) bool {
	return (seq2-seq1)>>31 != 0
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}

// for debug log
func RdpDebugLog(mode, format string, v ...interface{}) {
	if OpenDebugLog {
		switch mode {
		case "debug":
			debugF(format, v...)
		case "error":
			errorF(format, v...)
		}
	}
}
