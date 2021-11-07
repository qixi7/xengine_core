package xmetric

import (
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"
	"xcore/xconsul"
	"xcore/xlog"
	"xcore/xmodule"
)

const unknown = "unknown"

type labelConfig struct {
	host    string
	alias   string
	program string
	localip string
}

type Gather struct {
	labelConfig
	registry      *prometheus.Registry
	pullChan      chan MetricJob
	pushChan      chan MetricJob
	closeChan     chan struct{}
	jobType       reflect.Type
	mux           *http.ServeMux
	httpAddr      string
	httpPort      int
	consulAddress string
	dummyDescs    map[string]*prometheus.Desc
	xconsul.ConsulClient
	initOK bool // 是否注册成功
}

func (g *Gather) Init(selfGetter xmodule.DModuleGetter) bool {
	g.mux.Handle("/metrics", promhttp.HandlerFor(g.registry, promhttp.HandlerOpts{}))
	go func() {
		g.SetServiceID("host_" + g.localip + "_" + g.program)
		for !g.Register() {
			time.Sleep(time.Second * 10)
		}
		xlog.InfoF("http metric url: http://%s:%d/metrics", g.httpAddr, g.httpPort)
		xlog.InfoF("http health url: http://%s:%d/health", g.httpAddr, g.httpPort)
		g.initOK = true
	}()

	g.closeChan = make(chan struct{})
	g.pullChan = make(chan MetricJob)
	g.pushChan = make(chan MetricJob, 4)
	g.dummyDescs = make(map[string]*prometheus.Desc)
	g.registry.MustRegister(newDummyCollector(g))
	g.registry.MustRegister(newGoCollector(g))
	return true
}

func (g *Gather) Destroy() {
	if g.initOK {
		g.DeRegister()
	}
	close(g.closeChan)
}

func (g *Gather) Run(delta int64) {
	select {
	case job := <-g.pullChan:
		job.Pull()
		select {
		case g.pushChan <- job:
		default:
			return
		}
	default:
		return
	}
}

func (g *Gather) modGetDesc(name string, labels []string) *prometheus.Desc {
	namekey := name
	for _, v := range labels {
		namekey += "_" + v
	}
	desc, ok := g.dummyDescs[namekey]
	if ok {
		return desc
	}
	desc = prometheus.NewDesc(name, name, labels, g.defaultLabels())
	g.dummyDescs[namekey] = desc
	return desc
}

func (g *Gather) PushGaugeMetric(ch chan<- prometheus.Metric, name string, value float64, labels []string, labelValues ...string) {
	desc := g.modGetDesc(name, labels)
	mertic, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, value, labelValues...)
	if err != nil {
		xlog.Errorf("PushGaugeMetric, NewConstMetric err=%v", err)
		return
	}
	ch <- mertic
}

func (g *Gather) PushCounterMetric(ch chan<- prometheus.Metric, name string, value float64, labels []string, labelValues ...string) {
	desc := g.modGetDesc(name, labels)
	mertic, err := prometheus.NewConstMetric(desc, prometheus.CounterValue, value, labelValues...)
	if err != nil {
		xlog.Errorf("PushCounterMetric, NewConstMetric err=%v", err)
		return
	}
	ch <- mertic
}

func (g *Gather) Host(host string) *Gather {
	g.host = host
	return g
}

func (g *Gather) Alias(alias string) *Gather {
	g.alias = alias
	return g
}

func (g *Gather) Program(program string) *Gather {
	g.program = program
	return g
}

func (g *Gather) SetConsulCfg(cfg *xconsul.HTTPConfig) *Gather {
	g.HTTPConfig = cfg
	return g
}

func NewGather(regJob MetricJob, smux *http.ServeMux, addr string, port int) *Gather {
	kv1 := map[string]string{"TTL": "20s"}
	kv2 := map[string]string{
		"Interval":                       "10s",
		"HTTP":                           fmt.Sprintf("http://%s:%d/health", addr, port),
		"DeregisterCriticalServiceAfter": "30s",
	}
	g := &Gather{
		registry: prometheus.NewRegistry(),
		jobType:  reflect.TypeOf(regJob),
		mux:      smux,
		httpAddr: addr,
		httpPort: port,
		ConsulClient: xconsul.ConsulClient{
			Addr:        addr,
			Port:        port,
			Tags:        []string{"v1"},
			ServiceName: "gamemetric",
			Checks:      xconsul.AgentServiceChecksString(xconsul.AgentServiceCheckString(nil, kv1), xconsul.AgentServiceCheckString(nil, kv2)),
		},
	}
	g.defaultLabels()
	return g
}

func (g *Gather) defaultLabels() map[string]string {
	if len(g.program) == 0 {
		g.program = filepath.Base(os.Args[0])
	}
	if len(g.host) == 0 {
		g.host = getLocalAddr()
		if g.host == unknown {
			g.host = getHostName()
		}
	}
	if len(g.alias) == 0 {
		g.alias = unknown
	}
	if len(g.localip) == 0 {
		g.localip = getLocalAddr()
	}
	return map[string]string{"host": g.host, "alias": g.alias, "program": g.program}
}

func getLocalAddr() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return unknown
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok {
			if ipnet.IP.IsLoopback() {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			return ipnet.IP.String()
		}
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return unknown
}

func getHostName() string {
	host, err := os.Hostname()
	if err != nil {
		return unknown
	}
	return host
}
