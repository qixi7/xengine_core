package xutil

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"
)

/*
	服务器调试命令
*/

var units = []string{"bytes", "KB", "MB", "GB", "TB", "PB"}

func formatBytes(val uint64) string {
	var i int
	var target uint64
	for i = range units {
		target = 1 << uint(10*(i+1))
		if val < target {
			break
		}
	}
	if i > 0 {
		return fmt.Sprintf("%0.2f%s (%d bytes)",
			float64(val)/(float64(target)/1024), units[i], val)
	}
	return fmt.Sprintf("%d bytes", val)
}

func defaultCmd(app *Application) {
	app.HandleHttpCmd("/help", func(args []string) string {
		buffer := new(bytes.Buffer)
		for i := 0; i < len(app.httpCmds); i++ {
			buffer.WriteString(app.httpCmds[i])
			buffer.WriteString("\n")
		}
		return buffer.String()
	})

	app.HandleHttpCmd("/pprof/start/:name", func(args []string) string {
		if len(args) != 3 {
			return "arg error, should be: /pprof/start/mode(cpu, memory, ...)"
		}
		if app.profile.Start(args[2]) {
			return fmt.Sprintf("%s profile started", args[2])
		} else {
			return fmt.Sprintf("%s profile failed", args[2])
		}
	})

	app.HandleHttpCmd("/pprof/stop", func(args []string) string {
		if len(args) != 2 {
			return "arg error, should be: /pprof/stop"
		}
		app.profile.Stop()
		return "profile stopped"
	})

	app.serveMux.HandleFunc("/debug/stack", func(w http.ResponseWriter, r *http.Request) {
		err := pprof.Lookup("goroutine").WriteTo(w, 2)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
		}
	})

	app.serveMux.HandleFunc("/debug/mem", func(w http.ResponseWriter, r *http.Request) {
		var s runtime.MemStats
		runtime.ReadMemStats(&s)
		_, _ = fmt.Fprintf(w, "alloc: %v\n", formatBytes(s.Alloc))
		_, _ = fmt.Fprintf(w, "total-alloc: %v\n", formatBytes(s.TotalAlloc))
		_, _ = fmt.Fprintf(w, "sys: %v\n", formatBytes(s.Sys))
		_, _ = fmt.Fprintf(w, "lookups: %v\n", formatBytes(s.Lookups))
		_, _ = fmt.Fprintf(w, "mallocs: %v\n", formatBytes(s.Mallocs))
		_, _ = fmt.Fprintf(w, "frees: %v\n", formatBytes(s.Frees))
		_, _ = fmt.Fprintf(w, "heap-alloc: %v\n", formatBytes(s.HeapAlloc))
		_, _ = fmt.Fprintf(w, "heap-sys: %v\n", formatBytes(s.HeapSys))
		_, _ = fmt.Fprintf(w, "heap-idle: %v\n", formatBytes(s.HeapIdle))
		_, _ = fmt.Fprintf(w, "heap-in-use: %v\n", formatBytes(s.HeapInuse))
		_, _ = fmt.Fprintf(w, "heap-released: %v\n", formatBytes(s.HeapReleased))
		_, _ = fmt.Fprintf(w, "heap-object: %v\n", formatBytes(s.HeapObjects))
		_, _ = fmt.Fprintf(w, "stack-in-use: %v\n", formatBytes(s.StackInuse))
		_, _ = fmt.Fprintf(w, "stack-sys: %v\n", formatBytes(s.StackSys))
		_, _ = fmt.Fprintf(w, "stack-mspan-inuse: %v\n", formatBytes(s.MSpanInuse))
		_, _ = fmt.Fprintf(w, "stack-mspan-sys: %v\n", formatBytes(s.MSpanSys))
		_, _ = fmt.Fprintf(w, "stack-mcache-inuse: %v\n", formatBytes(s.MCacheInuse))
		_, _ = fmt.Fprintf(w, "stack-mcache-sys: %v\n", formatBytes(s.MCacheSys))
		_, _ = fmt.Fprintf(w, "other-sys: %v\n", formatBytes(s.OtherSys))
		_, _ = fmt.Fprintf(w, "gc-sys: %v\n", formatBytes(s.GCSys))
		_, _ = fmt.Fprintf(w, "next-gc: when heap-alloc >= %v\n", formatBytes(s.NextGC))
		lastGC := "-"
		if s.LastGC != 0 {
			lastGC = fmt.Sprint(time.Unix(0, int64(s.LastGC)))
		}
		_, _ = fmt.Fprintf(w, "last-gc: %v\n", lastGC)
		_, _ = fmt.Fprintf(w, "gc-pause-total: %v\n", time.Duration(s.PauseTotalNs))
		_, _ = fmt.Fprintf(w, "gc-pause: %v\n", s.PauseNs[(s.NumGC+255)%256])
		pausePeak := uint64(0)
		for _, pause := range s.PauseNs {
			if pausePeak < pause {
				pausePeak = pause
			}
		}
		_, _ = fmt.Fprintf(w, "gc-pause-peak: %v\n", pausePeak)
		_, _ = fmt.Fprintf(w, "num-gc: %v\n", s.NumGC)
		_, _ = fmt.Fprintf(w, "enable-gc: %v\n", s.EnableGC)
		_, _ = fmt.Fprintf(w, "debug-gc: %v\n", s.DebugGC)
	})

	app.serveMux.HandleFunc("/debug/goversion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%v\n", runtime.Version())
	})

	app.serveMux.HandleFunc("/debug/gc", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		runtime.GC()
		taken := time.Now().Sub(start)
		_, _ = w.Write([]byte("ok, " + taken.String() + "\n"))
	})

	app.serveMux.HandleFunc("/debug/stat", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "goroutines: %v\n", runtime.NumGoroutine())
		_, _ = fmt.Fprintf(w, "OS Threads: %v\n", pprof.Lookup("threadcreate").Count())
		_, _ = fmt.Fprintf(w, "GOMAXPROCS: %v\n", runtime.GOMAXPROCS(0))
		_, _ = fmt.Fprintf(w, "num CPU: %v\n", runtime.NumCPU())
	})

	app.serveMux.HandleFunc("/debug/dump", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Create("dump")
		if err != nil {
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		defer func() { _ = f.Close() }()
		start := time.Now()
		debug.WriteHeapDump(f.Fd())
		taken := time.Now().Sub(start)
		_, _ = w.Write([]byte("ok, " + taken.String() + "\n"))
	})
}
