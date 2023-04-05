package rdpkit

import (
	"sync/atomic"
)

var metrics Metric

type Metric struct {
	// 收发包
	SendBytes   uint64
	RecvBytes   uint64
	SendPackets uint64
	RecvPackets uint64

	UDPSendBytes   uint64
	UDPRecvBytes   uint64
	UDPSendPackets uint64
	UDPRecvPackets uint64

	// 连接数
	ConnectionNum int64

	// 发送阻塞重传次数
	WriteBlockRetransmit int64

	// 包体错误数
	CheckSumErr uint64
}

func (m *Metric) Pull() {
	*m = Metric{
		SendBytes:   atomic.LoadUint64(&metrics.SendBytes),
		RecvBytes:   atomic.LoadUint64(&metrics.RecvBytes),
		SendPackets: atomic.LoadUint64(&metrics.SendPackets),
		RecvPackets: atomic.LoadUint64(&metrics.RecvPackets),

		UDPSendBytes:   atomic.LoadUint64(&metrics.UDPSendBytes),
		UDPRecvBytes:   atomic.LoadUint64(&metrics.UDPRecvBytes),
		UDPSendPackets: atomic.LoadUint64(&metrics.UDPSendPackets),
		UDPRecvPackets: atomic.LoadUint64(&metrics.UDPRecvPackets),

		ConnectionNum:        atomic.LoadInt64(&metrics.ConnectionNum),
		WriteBlockRetransmit: atomic.LoadInt64(&metrics.WriteBlockRetransmit),
		CheckSumErr:          atomic.LoadUint64(&metrics.CheckSumErr),
	}
}
