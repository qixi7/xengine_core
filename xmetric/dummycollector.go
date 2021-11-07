package xmetric

import (
	"github.com/prometheus/client_golang/prometheus"
	"reflect"
	"time"
)

type dummyCollector struct {
	gather *Gather
}

func newDummyCollector(g *Gather) *dummyCollector {
	return &dummyCollector{gather: g}
}

func (c *dummyCollector) Describe(ch chan<- *prometheus.Desc) {
	goroutinesDesc := prometheus.NewDesc("dummy_desc", "dummy_desc", nil, c.gather.defaultLabels())
	ch <- goroutinesDesc
}

func (c *dummyCollector) Collect(ch chan<- prometheus.Metric) {
	job := reflect.New(c.gather.jobType.Elem()).Interface().(MetricJob)
	select {
	case c.gather.pullChan <- job:
		select {
		case retJob := <-c.gather.pushChan:
			retJob.Push(ch)
		}
	case <-time.After(2 * time.Second):
		return
	}
}
