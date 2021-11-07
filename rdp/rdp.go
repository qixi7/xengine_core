package rdp

import (
	"encoding/binary"
	"errors"
	"net"
	"time"
	"xcore/xlog"
)

/***
		RDP - Redundant Data Protocol
一、概述
	冗余传输协议。
	1).基于UDP. 算法实现
	2).通过冗余包实现可靠传输
	3).协议层摒弃定时器以更大提升性能, 依赖外部定时器
	4).更短的RTO. (TCP->指数增长, KCP->1.5倍增长)
	5).Ack+Select Ack更快确认机制
	6).支持链路探路探测控制冗余包粒度

二、缺点
	1).为了更快到达对端, 没有实现拥塞控制, 因此使用在小包传输场景。比如帧同步

三、TODO.
	1).根据探测结果动态调整冗余个数

四、协议序
	[协议头(1字节) + 包类型(1字节) + ack(4位) + ackBit(4位)]

*/

var (
	ErrMTUExceeded         = errors.New("MTU exceeded")
	ErrMaxLoadSizeExceeded = errors.New("maxLoadSize exceeded")
)

const (
	protocolID = byte(0x43)

	mtu   = 1200
	magic = uint32(0x5A11ADFC)

	// queueSize is the default size of send/receive window (in packets).
	queueSize = 256
	rttWindow = 4

	incomingChanSize      = 256
	socketReadBufferSize  = 8 * mtu
	socketWriteBufferSize = 2 * mtu

	maxLoadSize   = 300
	MaxPacketSize = maxLoadSize

	// 冗余包数
	RedundantNum = 5

	// debug log switch
	OpenDebugLog = false
)

func rawAddr(addr *net.UDPAddr) (raw [6]byte) {
	copy(raw[:4], addr.IP[:4])
	binary.BigEndian.PutUint16(raw[4:], uint16(addr.Port))
	return
}

func addrEquals(addr1, addr2 *net.UDPAddr) bool {
	return (addr1 == addr2) || (addr1.Port == addr2.Port) && addr1.IP.Equal(addr2.IP)
}

// seq2 after seq1
func after(seq2, seq1 uint32) bool {
	return (seq2-seq1)>>31 != 0
}

func now() int64 {
	return time.Now().UnixNano()
}

// for debug log
func RdpDebugLog(mode, format string, v ...interface{}) {
	if OpenDebugLog {
		switch mode {
		case "debug":
			xlog.Debugf(format, v)
		case "error":
			xlog.Error(format, v)
		}
	}
}
