package xprofile

import (
	"flag"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"sync/atomic"
	"xcore/xlog"
)

/*
	通过启动参数, 开启性能收集关服时生成profile性能分析文件. 支持 cpu, mem, trace, mutex, block
	eg: ./server -prof.mode=cpu,mem
*/

type Profiler struct {
	closer         []func()
	memProfileRate int
	status         uint32
}

var mode string

func init() {
	flag.StringVar(&mode, "prof.mode", "", "profile mode, such as \"cpu,mem\"")
}

func (p *Profiler) cpuProfile() {
	f, err := os.Create("cpu.pprof")
	if err != nil {
		xlog.Errorf("profiler: could not create cpu profile err=%v", err)
		return
	}
	xlog.InfoF("profiler: cpu profile enabled")
	if err := pprof.StartCPUProfile(f); err != nil {
		xlog.Errorf("profiler: could not start cpu profile err=%v", err)
		return
	}
	p.closer = append(p.closer, func() {
		pprof.StopCPUProfile()
		f.Close()
		xlog.InfoF("profiler: cpu profile disabled")
	})
}

func (p *Profiler) memProfile() {
	f, err := os.Create("mem.pprof")
	if err != nil {
		xlog.Errorf("profiler: could not create mem profile err=%v", err)
		return
	}
	xlog.InfoF("profiler: mem profile enabled")
	old := runtime.MemProfileRate
	runtime.MemProfileRate = p.memProfileRate
	p.closer = append(p.closer, func() {
		if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
			xlog.Errorf("profiler: could not start mem profile err=%v", err)
			return
		}
		f.Close()
		runtime.MemProfileRate = old
		xlog.InfoF("profiler: mem profile disabled")
	})
}

func (p *Profiler) mutexProfile() {
	f, err := os.Create("mutex.pprof")
	if err != nil {
		xlog.Errorf("profiler: could not create mutex profile err=%v", err)
		return
	}
	xlog.InfoF("profiler: mutex profile enabled")
	runtime.SetMutexProfileFraction(1)
	p.closer = append(p.closer, func() {
		if err := pprof.Lookup("mutex").WriteTo(f, 0); err != nil {
			xlog.Errorf("profiler: could not start mutex profile err=%v", err)
			return
		}
		f.Close()
		runtime.SetMutexProfileFraction(0)
		xlog.InfoF("profiler: mutex profile disabled")
	})
}

func (p *Profiler) blockProfile() {
	f, err := os.Create("block.pprof")
	if err != nil {
		xlog.Errorf("profiler: could not create block profile err=%v", err)
		return
	}
	xlog.InfoF("profiler: block profile enabled")
	runtime.SetBlockProfileRate(1)
	p.closer = append(p.closer, func() {
		if err := pprof.Lookup("block").WriteTo(f, 0); err != nil {
			xlog.Errorf("profiler: could not start block profile err=%v", err)
			return
		}
		f.Close()
		runtime.SetBlockProfileRate(0)
		xlog.InfoF("profiler: block profile disabled")
	})
}

func (p *Profiler) traceProfile() {
	f, err := os.Create("trace.out")
	if err != nil {
		xlog.Errorf("profiler: could not create trace profile err=%v", err)
		return
	}
	if err := trace.Start(f); err != nil {
		xlog.Errorf("profiler: could not start trace profile err=%v", err)
		return
	}
	xlog.InfoF("profiler: trace profile enabled")
	p.closer = append(p.closer, func() {
		trace.Stop()
		xlog.InfoF("profiler: block profile disabled")
	})
}

func (p *Profiler) Start(pmode string) bool {
	if len(pmode) == 0 {
		return false
	}
	mmodes := map[string]func(){
		"cpu":   p.cpuProfile,
		"mem":   p.memProfile,
		"mutex": p.mutexProfile,
		"block": p.blockProfile,
		"trace": p.traceProfile,
	}
	modes := strings.FieldsFunc(pmode, func(r rune) bool {
		return uint32(r) == ','
	})
	for _, m := range modes {
		_, ok := mmodes[m]
		if !ok {
			return false
		}
	}
	if !atomic.CompareAndSwapUint32(&p.status, 0, 1) {
		return false
	}
	p.memProfileRate = 4096

	for _, m := range modes {
		if f, ok := mmodes[m]; ok {
			f()
		}
	}
	return true
}

func (p *Profiler) Stop() {
	if !atomic.CompareAndSwapUint32(&p.status, 1, 0) {
		return
	}
	for _, c := range p.closer {
		c()
	}
	p.closer = nil
}

func Start() *Profiler {
	var prof Profiler
	prof.Start(mode)
	return &prof
}
