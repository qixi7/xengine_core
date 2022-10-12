package xlobby

import (
	"encoding/json"
	"fmt"
	"github.com/qixi7/xengine_core/xconsul"
	"github.com/qixi7/xengine_core/xlog"
	"github.com/qixi7/xengine_core/xmodule"
	"github.com/qixi7/xengine_core/xutil"
	"time"
)

type ILobbyRequestInfo interface {
	RequestInfo(args []string) string
}

type LobbyManager struct {
	program     string
	worldID     int
	requestInfo ILobbyRequestInfo
	project     string
	nodetype    string
	xconsul.ConsulClient
	initOK bool // 是否注册成功
}

type JLobbyNodeInfo struct {
	Project  string `json:"Project"`
	Program  string `json:"Program"`
	WorldID  int    `json:"WorldID"`
	NodeType string `json:"NodeType"`
	NodeData string `json:"NodeData"`
}

func NewLobbyManager(program string, req ILobbyRequestInfo, project string, worldID int, nodetype string) *LobbyManager {
	_, addr, port := xutil.DefaultServerMux()
	kv1 := map[string]string{
		"Interval": "10s",
		"HTTP":     fmt.Sprintf("http://%s:%d/health", addr, port),
		"Timeout":  "2s",
		//"DeregisterCriticalServiceAfter": "30s", // 暂时不删除, 删除后在断线情况下会有bug
	}
	return &LobbyManager{
		program:     program,
		requestInfo: req,
		project:     project,
		nodetype:    nodetype,
		worldID:     worldID,
		ConsulClient: xconsul.ConsulClient{
			HTTPConfig:  xutil.ConsulCfg(),
			Addr:        addr,
			Port:        port,
			Tags:        []string{"v1", project, fmt.Sprintf("%d", worldID)},
			ServiceID:   "lobbyhost_" + addr + "_" + program,
			ServiceName: "lobby",
			Check:       xconsul.AgentServiceCheckString(nil, kv1),
		},
	}
}

func (mgr *LobbyManager) Init(selfGetter xmodule.DModuleGetter) bool {
	xutil.GApp.HandleHttpCmd("/lobby", func(args []string) string {
		if mgr.requestInfo == nil {
			return ""
		}
		lbinfo := JLobbyNodeInfo{
			Project:  mgr.project,
			Program:  mgr.program,
			WorldID:  mgr.worldID,
			NodeType: mgr.nodetype,
			NodeData: mgr.requestInfo.RequestInfo(args),
		}
		data, err := json.MarshalIndent(lbinfo, "", "		")
		if err != nil {
			return ""
		}
		return string(data)
	})
	go func() {
		for !mgr.Register() {
			time.Sleep(10 * time.Second)
		}
		_, addr, port := xutil.DefaultServerMux()
		xlog.InfoF("http lobby url: http://%s:%d/lobby", addr, port)
		mgr.initOK = true
	}()

	return true
}

func (mgr *LobbyManager) Destroy() {
	if mgr.initOK {
		mgr.DeRegister()
	}
}

func (mgr *LobbyManager) Run(delta int64) {
}
