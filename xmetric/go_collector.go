package xmetric

import (
	"github.com/prometheus/client_golang/prometheus"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

type goCollector struct {
	gather *Gather

	goroutinesDesc *prometheus.Desc
	gcDesc         *prometheus.Desc

	// ms... are memstats related.
	msLast          *runtime.MemStats // Previously collected memstats.
	msLastTimestamp time.Time
	msMtx           sync.Mutex // Protects msLast and msLastTimestamp.
	msMetrics       memStatsMetrics
	msRead          func(*runtime.MemStats) // For mocking in tests.
	msMaxWait       time.Duration           // Wait time for fresh memstats.
	msMaxAge        time.Duration           // Maximum allowed age of old memstats.
}

func newGoCollector(g *Gather) *goCollector {
	return &goCollector{
		gather: g,
		goroutinesDesc: prometheus.NewDesc(
			"go_goroutines",
			"Number of goroutines that currently exist.",
			nil, g.defaultLabels()),
		gcDesc: prometheus.NewDesc(
			"go_gc_duration_seconds",
			"A summary of the pause duration of garbage collection cycles.",
			nil, g.defaultLabels()),
		msLast:    &runtime.MemStats{},
		msRead:    runtime.ReadMemStats,
		msMaxWait: time.Second,
		msMaxAge:  5 * time.Minute,
		msMetrics: memStatsMetrics{
			{
				desc: prometheus.NewDesc(
					memstatNamespace("sys_bytes"),
					"Number of bytes obtained from system.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.Sys) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("heap_sys_bytes"),
					"Number of heap bytes obtained from system.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.HeapSys) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("heap_inuse_bytes"),
					"Number of heap bytes that are in use.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.HeapInuse) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("heap_objects"),
					"Number of allocated objects.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.HeapObjects) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("stack_sys_bytes"),
					"Number of bytes obtained from system for stack allocator.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.StackSys) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("stack_inuse_bytes"),
					"Number of bytes in use by the stack allocator.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.StackInuse) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("next_gc_bytes"),
					"Number of heap bytes when next garbage collection will take place.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.NextGC) },
				valType: prometheus.GaugeValue,
			}, {
				desc: prometheus.NewDesc(
					memstatNamespace("last_gc_time_seconds"),
					"Number of seconds since 1970 of last garbage collection.",
					nil, g.defaultLabels(),
				),
				eval:    func(ms *runtime.MemStats) float64 { return float64(ms.LastGC) / 1e9 },
				valType: prometheus.GaugeValue,
			},
		},
	}
}

func memstatNamespace(s string) string {
	return "go_memstats_" + s
}

// Describe returns all descriptions of the collector.
func (c *goCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.goroutinesDesc
	ch <- c.gcDesc
	for _, i := range c.msMetrics {
		ch <- i.desc
	}
}

// Collect returns the current state of all metrics of the collector.
func (c *goCollector) Collect(ch chan<- prometheus.Metric) {
	var (
		ms   = &runtime.MemStats{}
		done = make(chan struct{})
	)
	// Start reading memstats first as it might take a while.
	go func() {
		c.msRead(ms)
		c.msMtx.Lock()
		c.msLast = ms
		c.msLastTimestamp = time.Now()
		c.msMtx.Unlock()
		close(done)
	}()

	ch <- prometheus.MustNewConstMetric(c.goroutinesDesc, prometheus.GaugeValue, float64(runtime.NumGoroutine()))

	var stats debug.GCStats
	stats.PauseQuantiles = make([]time.Duration, 5)
	debug.ReadGCStats(&stats)

	quantiles := make(map[float64]float64)
	for idx, pq := range stats.PauseQuantiles[1:] {
		quantiles[float64(idx+1)/float64(len(stats.PauseQuantiles)-1)] = pq.Seconds()
	}
	quantiles[0.0] = stats.PauseQuantiles[0].Seconds()
	ch <- prometheus.MustNewConstSummary(c.gcDesc, uint64(stats.NumGC), stats.PauseTotal.Seconds(), quantiles)

	timer := time.NewTimer(c.msMaxWait)
	select {
	case <-done: // Our own ReadMemStats succeeded in time. Use it.
		timer.Stop() // Important for high collection frequencies to not pile up timers.
		c.msCollect(ch, ms)
		return
	case <-timer.C: // Time out, use last memstats if possible. Continue below.
	}
	c.msMtx.Lock()
	if time.Since(c.msLastTimestamp) < c.msMaxAge {
		// Last memstats are recent enough. Collect from them under the lock.
		c.msCollect(ch, c.msLast)
		c.msMtx.Unlock()
		return
	}
	// If we are here, the last memstats are too old or don't exist. We have
	// to wait until our own ReadMemStats finally completes. For that to
	// happen, we have to release the lock.
	c.msMtx.Unlock()
	<-done
	c.msCollect(ch, ms)
}

func (c *goCollector) msCollect(ch chan<- prometheus.Metric, ms *runtime.MemStats) {
	for _, i := range c.msMetrics {
		ch <- prometheus.MustNewConstMetric(i.desc, i.valType, i.eval(ms))
	}
}

// memStatsMetrics provide description, value, and value type for memstat metrics.
type memStatsMetrics []struct {
	desc    *prometheus.Desc
	eval    func(*runtime.MemStats) float64
	valType prometheus.ValueType
}
