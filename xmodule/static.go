package xmodule

/**
static 模块. 放静态不动的数据. 比如策划档, 服务器配置
*/

import "github.com/qixi7/xengine_core/xlog"

type SModule interface {
	Load() bool
	Reload()
	Destroy()
}

type SModuleMgr []SModule

func NewSModuleMgr(num int) SModuleMgr {
	return make([]SModule, num)
}

type SModuleGetter struct {
	mgr *SModuleMgr // note: mgr is the addr of root and will not be changed after hotfix
	id  int
}

func (g SModuleGetter) Get() SModule {
	return (*g.mgr)[g.id]
}

func (mgr *SModuleMgr) Register(id int, m SModule) SModuleGetter {
	(*mgr)[id] = m
	return SModuleGetter{mgr: mgr, id: id}
}

func (mgr *SModuleMgr) Getter(id int) SModuleGetter {
	return SModuleGetter{mgr: mgr, id: id}
}

func (mgr *SModuleMgr) Load() bool {
	for n, m := range *mgr {
		if m != nil && !m.Load() {
			xlog.Fatalf("data module [%d] load failed", n)
			return false
		}
	}
	return true
}

func (mgr *SModuleMgr) Destroy() {
	for n, m := range *mgr {
		xlog.Fatalf("data module [%d] destroy", n)
		m.Destroy()
	}
}

func (mgr *SModuleMgr) Reload(id int) bool {
	if (*mgr)[id] == nil {
		xlog.Errorf("Reload not register ID=%d", id)
		return false
	}
	(*mgr)[id].Reload()
	return true
}
