package rdp

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/qixi7/xengine_core/xmetric"
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

	// 丢包情况
	InitiativeDrop int64 // 主动丢包

	// 发送阻塞重传次数
	WriteBlockRetransmit int64
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
		InitiativeDrop:       atomic.LoadInt64(&metrics.InitiativeDrop),
		WriteBlockRetransmit: atomic.LoadInt64(&metrics.WriteBlockRetransmit),
	}
}

func (m *Metric) Push(gather *xmetric.Gather, ch chan<- prometheus.Metric) {
	gather.PushCounterMetric(ch, "rdp_logical_bytes_sent",
		float64(m.SendBytes), nil)
	gather.PushCounterMetric(ch, "rdp_logical_bytes_recv",
		float64(m.RecvBytes), nil)
	gather.PushCounterMetric(ch, "rdp_logical_packets_sent",
		float64(m.SendPackets), nil)
	gather.PushCounterMetric(ch, "rdp_logical_packets_recv",
		float64(m.RecvPackets), nil)

	gather.PushCounterMetric(ch, "rdp_bottom_bytes_sent",
		float64(m.UDPSendBytes), nil)
	gather.PushCounterMetric(ch, "rdp_bottom_bytes_recv",
		float64(m.UDPRecvBytes), nil)
	gather.PushCounterMetric(ch, "rdp_bottom_packet_sent",
		float64(m.UDPSendPackets), nil)
	gather.PushCounterMetric(ch, "rdp_bottom_packet_recv",
		float64(m.UDPRecvPackets), nil)

	gather.PushGaugeMetric(ch, "rdp_connection_num",
		float64(m.ConnectionNum), nil)

	gather.PushCounterMetric(ch, "rdp_drop_initiative",
		float64(m.InitiativeDrop), nil)

	gather.PushCounterMetric(ch, "rdp_retran_writeblock",
		float64(m.WriteBlockRetransmit), nil)
}
