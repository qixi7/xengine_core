package xutil

import (
	"flag"
	"fmt"
	"github.com/bmizerany/pat"
	"github.com/prometheus/client_golang/prometheus"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"xcore/xconsul"
	"xcore/xlog"
	"xcore/xmetric"
	"xcore/xprofile"
)

var (
	versionFlag    bool                   // 本次启动是否只为查看版本信息
	httpListenAddr string                 // 监听的http地址
	pidFile        string                 // pid文件
	GApp           = newApplication()     // app
	consulCfg      = xconsul.HTTPConfig{} // consul配置. 用于性能收集, lobby负载
)

const (
	maxPortRange = 64 // 最大端口偏移值. 比如配置监听13000, 先检测13000是否有被使用, 如果被使用就监听13001, 直到试n次累加
	unKnown      = "unknown"
)

// 最底层app. 一个进程就是一个app
type Application struct {
	exit         bool                 // 是否发起了退出
	fps          int                  // 服务器逻辑帧率. 1s 跑fps次 主线程update
	profile      *xprofile.Profiler   // profile
	httpAddr     string               // 监听的http地址
	httpPort     int                  // 监听的http端口
	serveMux     *http.ServeMux       // 用于http监听
	pattern      *pat.PatternServeMux // 三方库, 优化路由
	http2main    chan func()          // 放置http func, 在主线程中执行
	main2http    chan string          // 主线程把http函数执行的结果返回给http, 由http返回结果给客户端
	httpCmds     []string             // 定义的所有http命令, 用于/help返回给调用端本进程支持哪些命令
	InitOKTime   time.Time            // 初始化完成的时间点
	invokeOnMain chan struct{}        // 调用invokeFunc的信号
	invokeFunc   func()               // 需要主线程立即执行的函数, 不要等到下一个逻辑帧再执行
	metric       Metric               // 性能收集
}

// new app
func newApplication() *Application {
	return &Application{
		serveMux:     http.NewServeMux(),
		pattern:      pat.New(),
		http2main:    make(chan func(), 1024),
		main2http:    make(chan string, 1),
		invokeOnMain: make(chan struct{}, 1),
	}
}

// init
func init() {
	flag.BoolVar(&versionFlag, "v", false, "version")
	flag.StringVar(&httpListenAddr, "http.listen", "0.0.0.0:13000", "http listen addr")
	flag.StringVar(&consulCfg.HttpAddr, "consul.httpAddr", "http://0.0.0.0:8500", "consul http addr")
	// default pid file name is executable file name with .pid suffix
	exe, _ := os.Executable()
	exeNameOnly := strings.TrimSuffix(filepath.Base(exe), ".exe")
	flag.StringVar(&pidFile, "pid", exeNameOnly+".pid", "pid file name")
}

// 把ip地址拆分成ip+port
func splitAddr(addr string) (string, int) {
	arr := strings.Split(addr, ":")
	if len(arr) != 2 {
		return "", 0
	}
	port, err := strconv.Atoi(arr[1])
	if err != nil {
		return "", 0
	}
	return arr[0], port
}

// 获取本地ip地址
func getLocalAddr() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return unKnown
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
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return unKnown
}

// http函数定义
type HttpCmdFunc func(args []string) string

// 设置事件驱动. 有的任务可能想本帧就立即执行, 而不等到下一次逻辑帧
func (app *Application) SetEvenDriverMode(f func()) *Application {
	if app.metric.FrameNum > 0 {
		panic("Event drive mode must be set before GApp.Run()")
	}
	app.invokeFunc = f
	return app
}

// 是否事件驱动
func (app *Application) IsEvenDriverMode() bool {
	return app.invokeFunc != nil
}

// 发送invokeOnMain事件(此函数不应在主线程调用)
func (app *Application) InvokeFuncOnMain() {
	if !app.IsEvenDriverMode() {
		panic("Event drive mode is not enable")
	}
	select {
	case app.invokeOnMain <- struct{}{}:
	default:
	}
}

// 监听http服务
func (a *Application) serveHTTP() bool {
	var err error
	a.httpAddr, a.httpPort = splitAddr(httpListenAddr)
	if a.httpAddr == "" {
		xlog.Errorf("http listen address wrong %s", httpListenAddr)
		return false
	}
	if a.httpAddr == "0.0.0.0" || a.httpAddr == "" {
		a.httpAddr = getLocalAddr()
	}
	// 找一个可用端口
	find := false
	var l net.Listener
	for i := a.httpPort; i < a.httpPort+maxPortRange; i++ {
		ipaddr := fmt.Sprintf("%s:%d", a.httpAddr, i)
		l, err = net.Listen("tcp", ipaddr)
		if err == nil {
			find = true
			a.httpPort = i
			xlog.InfoF("http listen  address: http://%s", ipaddr)
			break
		}
	}

	if !find {
		xlog.Errorf("http listen address error")
		return false
	}

	// 写下监听的http地址到文件
	if err = ioutil.WriteFile("httpaddr.conf", []byte(fmt.Sprintf("http://%s:%d", a.httpAddr, a.httpPort)), 0644); err != nil {
		xlog.Errorf("WriteFile httpaddr.conf err=%v", err)
	}

	// [/health] 路由主要用来应付consul的心跳检测.
	// consul会每隔一段时间访问一下注册服务的机器的/health接口, 如果成功访问了, consul就认为该机器注册的服务是可用的
	a.pattern.Get("/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "hello world")
	}))
	a.serveMux.Handle("/", a.pattern)
	go func() {
		err := http.Serve(l, a.serveMux)
		if err != nil {
			xlog.Errorf("serveHTTP, http.Serve err=%v", err)
		}
	}()
	return true
}

// 初始化
func (a *Application) Init(f func() bool) bool {
	// runtime.AssertMain()
	flag.Parse()
	// 如果本次启动只是为了查看版本, 打印版本信息后退出进程
	if versionFlag {
		fmt.Printf("version		: %s.%s.%s\n", MajorVersion, MinorVersion, PatchVersion)
		fmt.Printf("gitVersion	: %s\n", GitVersion)
		fmt.Printf("buildType	: %s\n", BuildType)
		fmt.Printf("buildTime	: %s\n", BuildTime)
		os.Exit(0)
	}
	a.profile = xprofile.Start()
	_, err := NewFlock(pidFile)
	if err != nil {
		xlog.Errorf("Application Init NewFlock err=%v", err)
		return false
	}
	if f == nil {
		return true
	}
	// 监听http
	if !a.serveHTTP() {
		return false
	}
	// 用一个协程监听退出信号, 保底退出
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		xlog.InfoF("caught signal: %v", <-c)
		a.exit = true
		time.Sleep(1 * time.Minute)
		var buf [65536]byte
		n := runtime.Stack(buf[:], true)
		xlog.Errorf("server not stopped in 1 minute, all stack is:\n%s", string(buf[:n]))
		time.Sleep(2 * time.Second) // 2s 后强制关闭
		os.Exit(1)
	}()

	ret := f()
	if !ret {
		a.profile.Stop()
		xlog.Sync()
		xlog.ZapSync()
	}

	// must write stdout
	fmt.Println("start: ", time.Now())
	defaultCmd(a)

	a.InitOKTime = time.Now()
	return ret
}

// run
func (a *Application) Run(f func(int64)) {
	// runtime.AssertMain()

	t := time.Duration(int64(time.Millisecond) * int64(1000) / int64(a.fps))
	// 单位保留到ms. 减少计算精度
	t = t / time.Millisecond * time.Millisecond
	lastTime := time.Now()
	ticker := time.NewTicker(t)
	defer ticker.Stop()
mainLoop:
	for {
		select {
		case <-ticker.C:
			if a.exit {
				break mainLoop
			}
			// monotonic time here
			nowTime := time.Now()
			dt := nowTime.Sub(lastTime)
			f(int64(dt))
			lastTime = nowTime
			a.metric.FrameNum++
			frameTime := time.Now().Sub(nowTime)
			a.metric.FrameNowTime = frameTime
			a.metric.FrameTime += a.metric.FrameNowTime
			if a.metric.FrameNowTime > t {
				a.metric.FrameOvertimeUse++
			}
		case httpfn := <-a.http2main:
			httpfn()
		httpfor:
			for {
				select {
				case httpfn := <-a.http2main:
					httpfn()
				default:
					break httpfor
				}
			}
		case <-a.invokeOnMain:
			if a.invokeFunc != nil {
				a.invokeFunc()
			}
		}
	}
}

func (a *Application) Destroy(f func()) {
	f()
	a.profile.Stop()
	xlog.Sync()
	xlog.ZapSync()
	xlog.Close()
}

func (a *Application) TickTotal() int64 {
	return a.metric.FrameNum
}

func (a *Application) SetFPS(needFps int) *Application {
	if needFps <= 0 || needFps > 1000 {
		needFps = 1000
	}
	a.fps = needFps
	return a
}

func (a *Application) SetVersion(major, minor, patch int) *Application {
	MajorVersion = strconv.Itoa(major)
	MinorVersion = strconv.Itoa(minor)
	PatchVersion = strconv.Itoa(patch)
	return a
}

// 自定义注册一些http命令
func (a *Application) HandleHttpCmd(pattern string, cmdfunc HttpCmdFunc) {
	a.httpCmds = append(a.httpCmds, pattern)
	a.pattern.Get(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/lobby" {
			xlog.InfoF("httpcmd:%s", r.RequestURI)
		}
		args := strings.FieldsFunc(r.RequestURI, func(r rune) bool {
			return uint32(r) == '/'
		})
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case a.http2main <- func() {
			a.main2http <- cmdfunc(args)
		}:
		case <-timer.C:
			_, _ = fmt.Fprintln(w, "main thread timeout")
			return
		}
		ret := <-a.main2http
		_, _ = fmt.Fprintln(w, ret)
	}))
}

// 退出进程
func (a *Application) Exit() {
	a.exit = true
}

// 默认ServerMux
func DefaultServerMux() (*http.ServeMux, string, int) {
	return GApp.serveMux, GApp.httpAddr, GApp.httpPort
}

// 获取consul config
func ConsulCfg() *xconsul.HTTPConfig {
	return &consulCfg
}

type Metric struct {
	FrameNum         int64
	FrameTime        time.Duration
	FrameNowTime     time.Duration
	FrameOvertimeUse int64
}

func (m *Metric) Pull() {
	*m = GApp.metric
}

func (m *Metric) Push(gather *xmetric.Gather, ch chan<- prometheus.Metric) {
	gather.PushCounterMetric(ch, "game_frame_total_num", float64(m.FrameNum), nil)
	gather.PushCounterMetric(ch, "game_frame_total_time", float64(m.FrameTime), nil)
	gather.PushGaugeMetric(ch, "game_frame_now_time", float64(m.FrameNowTime), nil)
	gather.PushCounterMetric(ch, "game_frame_overtime_num", float64(m.FrameOvertimeUse), nil)
}
