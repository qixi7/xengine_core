package xhlink

import (
	"os"
	"time"
	"xcore/xlog"
	"xcore/xmodule"
	"xcore/xnet"
)

type HLinker interface {
	xnet.Watcher
	xnet.PacketFormater
}

type HLinkUnitConfig struct {
	Addr   string
	Linker HLinker
}

type HLinkConfig struct {
	RouteID   int32 // 路由ID
	Name      string
	ListenMap map[string]*HLinkUnitConfig
	DialMap   map[string]*HLinkUnitConfig
}

// new
func NewHLinkConfig(nodeName string, routeID int32, listen, dial []*HLinkUnitConfig) *HLinkConfig {
	hcfg := &HLinkConfig{
		RouteID:   routeID,
		Name:      nodeName,
		ListenMap: make(map[string]*HLinkUnitConfig),
		DialMap:   make(map[string]*HLinkUnitConfig),
	}

	for idx, info := range listen {
		if _, ok := hcfg.ListenMap[info.Addr]; ok {
			xlog.Errorf("NewHLinkConfig init ListenMap err, infoAddr=%s, repeated.", info.Addr)
			os.Exit(8)
		}
		hcfg.ListenMap[info.Addr] = listen[idx]
	}

	for idx, info := range dial {
		if _, ok := hcfg.DialMap[info.Addr]; ok {
			xlog.Errorf("NewHLinkConfig init DialMap err, infoAddr=%s, repeated.", info.Addr)
			os.Exit(8)
		}
		hcfg.DialMap[info.Addr] = dial[idx]
	}

	return hcfg
}

// HLinkMgr Hotload Manager
type HLinkMgr struct {
	config *HLinkConfig
	ctx    *xnet.Context
}

func NewHLinkMgr(config *HLinkConfig) *HLinkMgr {
	ctx := xnet.NewContext()
	ctx.Timeout = 10 * time.Second
	return &HLinkMgr{
		config: config,
		ctx:    ctx,
	}
}

func (mgr *HLinkMgr) AddDial(dials []*HLinkUnitConfig) {
	if len(dials) == 0 {
		return
	}
	for i := 0; i < len(dials); i++ {
		if _, ok := mgr.config.DialMap[dials[i].Addr]; ok {
			xlog.Errorf("Add PostDial err! Addr=[%s] already exist!", dials[i].Addr)
			continue
		}
		xlog.InfoF("Add PostDial Addr=[%s]", dials[i].Addr)
		mgr.config.DialMap[dials[i].Addr] = dials[i]
		mgr.ctx.PostDial(dials[i].Addr, dials[i].Linker, dials[i].Linker, mgr.config.RouteID, mgr.config.Name)
	}
}

func (mgr *HLinkMgr) DelDial(delDial []*HLinkUnitConfig) {
	if len(delDial) == 0 {
		return
	}
	for i := 0; i < len(delDial); i++ {
		xlog.InfoF("Del PostDial Addr=[%s]", delDial[i].Addr)
		delete(mgr.config.DialMap, delDial[i].Addr)
		mgr.ctx.CloseDialConnection(delDial[i].Addr)
	}
}

func (mgr *HLinkMgr) CtxTick() {
	if mgr.ctx != nil {
		mgr.ctx.Tick()
	}
}

func (mgr *HLinkMgr) Init(selfGetter xmodule.DModuleGetter) bool {
	mgr.initLink()
	return true
}

func (mgr *HLinkMgr) initLink() {
	if len(mgr.config.ListenMap) > 0 {
		for _, cfg := range mgr.config.ListenMap {
			xlog.InfoF("listen Addr=[%s]", cfg.Addr)
			mgr.ctx.Listen(cfg.Addr, cfg.Linker, cfg.Linker, mgr.config.RouteID, mgr.config.Name)
		}
	}
	if len(mgr.config.DialMap) > 0 {
		for _, cfg := range mgr.config.DialMap {
			xlog.InfoF("PostDial Addr=[%s]", cfg.Addr)
			mgr.ctx.PostDial(cfg.Addr, cfg.Linker, cfg.Linker, mgr.config.RouteID, mgr.config.Name)
		}
	}
}

func (mgr *HLinkMgr) Run(dt int64) {
	mgr.ctx.Tick()
}

func (mgr *HLinkMgr) Destroy() {
	mgr.ctx.StopAll()
}

type Metric struct {
	xnet.SafeLinkMetric
}

func (m *Metric) Pull(mgr *HLinkMgr) {
	m.SafeLinkMetric.Pull(mgr.ctx)
}
