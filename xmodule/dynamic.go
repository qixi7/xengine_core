package xmodule

/**
dynamic 模块. 放动态更新的游戏逻辑模块. 比如玩家管理模块,房间管理模块,匹配模块
*/

import (
	"github.com/qixi7/xengine_core/xlog"
	"reflect"
	"strings"
	"time"
)

type DModule interface {
	Init(selfGetter DModuleGetter) bool
	Destroy()
	Run(delta int64)
}

type implDmodule struct {
	module        DModule
	tickUseTime   time.Duration
	tickTimeTotal time.Duration
	moduleName    string
}

type DModuleMgr []implDmodule

func NewDModuleMgr(num int) DModuleMgr {
	return make([]implDmodule, num)
}

type DModuleGetter struct {
	mgr *DModuleMgr
	id  int
}

func (g DModuleGetter) Get() DModule {
	return (*g.mgr)[g.id].module
}

func (mgr *DModuleMgr) Register(id int, m DModule) DModuleGetter {
	(*mgr)[id].module = m
	mName := strings.Replace(reflect.TypeOf(m).String(), "*", "_", -1)
	mName = "module" + strings.Replace(mName, ".", "_", -1)
	(*mgr)[id].moduleName = mName
	return mgr.Getter(id)
}

func (mgr *DModuleMgr) Getter(id int) DModuleGetter {
	return DModuleGetter{mgr: mgr, id: id}
}

func (mgr *DModuleMgr) Init(id int) bool {
	if id < 0 || id >= len(*mgr) {
		return false
	}

	if !(*mgr)[id].module.Init(mgr.Getter(id)) {
		xlog.Fatalf("dynamic module [%d] init failed", id)
		return false
	}

	return true
}

func (mgr *DModuleMgr) InitAll() bool {
	for n, m := range *mgr {
		if m.module != nil {
			gettler := mgr.Getter(n)
			if !m.module.Init(gettler) {
				xlog.Fatalf("dynamic module [%d:%s] init failed", n, m.moduleName)
				return false
			}
		}
	}
	return true
}

func (mgr *DModuleMgr) Destroy(id int) {
	if id < 0 || id >= len(*mgr) {
		return
	}
	xlog.InfoF("dynamic module [%d] destroy", id)
	(*mgr)[id].module.Destroy()
}

func (mgr *DModuleMgr) DestroyAll() {
	for n, m := range *mgr {
		if m.module != nil {
			xlog.InfoF("dynamic module [%d:%s] destroy", n, m.moduleName)
			m.module.Destroy()
		}
	}
}

func moduleRunOnce(m *implDmodule, delta int64) {
	if m.module != nil {
		nowTime := time.Now()
		m.module.Run(delta)
		m.tickUseTime = time.Now().Sub(nowTime)
		m.tickTimeTotal += m.tickUseTime
	}
}

func (mgr *DModuleMgr) Run(id int, delta int64) {
	if id < 0 || id >= len(*mgr) {
		return
	}
	m := &(*mgr)[id]
	moduleRunOnce(m, delta)
}

func (mgr *DModuleMgr) RunAll(delta int64) {
	for i := 0; i < len(*mgr); i++ {
		m := &(*mgr)[i]
		moduleRunOnce(m, delta)
	}
}

func (mgr *DModuleMgr) ForEachModuleMetric(runFunc func(name string, now time.Duration, total time.Duration)) {
	for i := 0; i < len(*mgr); i++ {
		m := &(*mgr)[i]
		if m.module != nil {
			runFunc(m.moduleName, m.tickUseTime, m.tickTimeTotal)
		}
	}
}
