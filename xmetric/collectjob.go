package xmetric

import (
	"github.com/prometheus/client_golang/prometheus"
)

type MetricJob interface {
	Pull()
	Push(ch chan<- prometheus.Metric)
}
