package xrpc

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"xcore/xhlink"
	"xcore/xnet"
)

// 非默认rpc连接协议定制
type HLinkUnitCfgCreator struct {
	NamePrefix string
	Creator    func(addr string) *xhlink.HLinkUnitConfig
}

// 集群配置. 通过 json 读取.
type clusterConfig struct {
	Cluster map[string]string   `json:"cluster"` // name->addr (addr || listen;dial)
	Server  map[string]string   `json:"server"`  // name->addr (addr || listen;dial)
	Client  map[string][]string `json:"client"`  // name->[]servername || []{"server}
}

// 节点信息
type NodeInfo struct {
	nodeName string // 节点名
	Addr     string // 地址
}

type ClusterCfg struct {
	Server         map[string]NodeInfo   // Server配置. 监听自己+连接其他Server
	serverAddrSort []NodeInfo            // listenaddr 大的连接小的, 保证cluster双方仅一个连接
	Self           string                // 自己的地址
	specialLinker  []HLinkUnitCfgCreator // 自己定制的rpc协议
}

// 从[addrs]里查找是否有[addr]
func sliceFindServerAddr(addr NodeInfo, addrs []NodeInfo) int {
	for i := 0; i < len(addrs); i++ {
		if addrs[i] == addr {
			return i
		}
	}
	return -1
}

// 把cluster map转化为 slice
func getClusterAddrSort(cluster map[string]NodeInfo) []NodeInfo {
	addrSort := make([]NodeInfo, 0, len(cluster))
	for _, addr := range cluster {
		addrSort = append(addrSort, addr)
	}
	sort.Slice(addrSort, func(i, j int) bool {
		return addrSort[i].Addr < addrSort[j].Addr
	})
	return addrSort
}

// 通过节点名+地址生成ServerAddr
func getClusterServerAddr(nodename, nodeaddr string) NodeInfo {
	return NodeInfo{
		nodeName: nodename,
		Addr:     nodeaddr,
	}
}

// 查重
func checkSetRepeatedServerAddr(nametrim string, addrmap map[string]string, srvAddr NodeInfo) (error, map[string]string) {
	if findname, ok := addrmap[srvAddr.Addr]; ok {
		return errors.New(nametrim + " same listen addr with " + findname), addrmap
	}
	addrmap[srvAddr.Addr] = nametrim
	return nil, addrmap
}

// 整理集群配置信息
func checkAndRearrangeClusterCfg(jsonCfgRead *clusterConfig, selfname string, specialLinker []HLinkUnitCfgCreator) (error, *ClusterCfg) {
	cfg := &ClusterCfg{
		Server:        make(map[string]NodeInfo),
		Self:          selfname,
		specialLinker: specialLinker,
	}
	addrtmp := make(map[string]string)
	allservers := make([]string, 0, len(jsonCfgRead.Server))
	// 遍历处理【Server】配置
	for nodename, nodeaddr := range jsonCfgRead.Server {
		nametrim := strings.Trim(nodename, " ")
		srvAddr := getClusterServerAddr(nametrim, nodeaddr)
		cfg.Server[nametrim] = srvAddr
		var err error = nil
		err, addrtmp = checkSetRepeatedServerAddr(nametrim, addrtmp, srvAddr)
		if err != nil {
			return err, nil
		}
		allservers = append(allservers, nametrim)
	}

	cfg.serverAddrSort = getClusterAddrSort(cfg.Server)
	return nil, cfg
}

// 通过集群json配置初始化集群配置信息
func LoadClusterFile(clusterfile string, selfname string, specialLinker []HLinkUnitCfgCreator) (error, *ClusterCfg) {
	f, err := os.Open(clusterfile)
	if err != nil {
		return err, nil
	}
	jsonStr, err := ioutil.ReadAll(f)
	if err != nil {
		return err, nil
	}
	f.Close()
	var jsonCfgRead clusterConfig
	if err = json.Unmarshal(jsonStr, &jsonCfgRead); err != nil {
		return err, nil
	}
	return checkAndRearrangeClusterCfg(&jsonCfgRead, selfname, specialLinker)
}

// new link with config
func (rs *RPCStatic) newClusterLink(nodename, addr string, cluster *ClusterCfg) *xhlink.HLinkUnitConfig {
	// 如果该link是使用了自定义link初始化函数, 就调用自定义初始化函数. 如不是, 创建默认rpc协议
	for _, linker := range cluster.specialLinker {
		if strings.HasPrefix(nodename, linker.NamePrefix) {
			return linker.Creator(addr)
		}
	}
	return rs.newRPCHLink(addr)
}

// 创建进程所有集群连接配置, 用于HLinkMgr初始化
func (rs *RPCStatic) NewClusterHLinkFromFile(clusterfile, selfname string, specialLinker []HLinkUnitCfgCreator) ([]*xhlink.HLinkUnitConfig, []*xhlink.HLinkUnitConfig, error) {
	err, cfg := LoadClusterFile(clusterfile, selfname, specialLinker)
	if err != nil {
		return nil, nil, err
	}
	selfServerAddr, selfServer := cfg.Server[cfg.Self]
	newlistens := []*xhlink.HLinkUnitConfig{} // 需要listen的地址
	newdials := []*xhlink.HLinkUnitConfig{}   // 需要dial的地址
	// 处理【Server】配置. Server监听自己并连接其他Server. 该方案可在这里修改
	if selfServer {
		newlistens = append(newlistens, rs.newClusterLink(selfServerAddr.nodeName, selfServerAddr.Addr, cfg))
		// Server之间互相连接
		for _, nodeaddr := range cfg.serverAddrSort {
			if selfServerAddr == nodeaddr {
				// 这里有个小秘密:
				// 为了防止多个连接dial和listen的混乱, 这里配合cfg.serverAddrSort
				// 来实现"大的addr"连接"小的addr", 保证cluster双方仅一个连接
				break
			}
			newdials = append(newdials, rs.newClusterLink(nodeaddr.nodeName, nodeaddr.Addr, cfg))
		}
	} else {
		// 不是Server就去连所有Server
		for _, nodeaddr := range cfg.Server {
			newdials = append(newdials, rs.newClusterLink(nodeaddr.nodeName, nodeaddr.Addr, cfg))
		}
	}
	rs.cfg = cfg
	return newlistens, newdials, nil
}

// 必须之前已经加载过集群, 用于重加载
// 支持增加、删除、修改节点
// 返回新增listen和dial
func (rs *RPCStatic) ReloadClusterHLinkFromFile(clusterfile, selfname string) ([]*xhlink.HLinkUnitConfig, []*xhlink.HLinkUnitConfig, error) {
	if rs.cfg == nil {
		return nil, nil, errors.New("no init cfg in ReloadClusterHLinkFromFile")
	}
	oldCfg := rs.cfg
	err, newCfg := LoadClusterFile(clusterfile, selfname, oldCfg.specialLinker)
	if err != nil {
		return nil, nil, err
	}
	if oldCfg.Self != newCfg.Self {
		return nil, nil, errors.New(fmt.Sprintf("Reload cluster selfname different [%s] != [%s]", oldCfg.Self, newCfg.Self))
	}
	selfServerAddr, selfServer := newCfg.Server[newCfg.Self]
	// 找出新增的和删除的
	newDial := []*xhlink.HLinkUnitConfig{}
	delDial := []*xhlink.HLinkUnitConfig{}
	fillDialFunc := func(traverse, contrast *ClusterCfg, fill *[]*xhlink.HLinkUnitConfig, server bool) {
		// traverse=遍历组, contrast=比对组
		for _, tmpaddr := range traverse.serverAddrSort {
			if server && selfServerAddr == tmpaddr {
				break
			}
			if sliceFindServerAddr(tmpaddr, contrast.serverAddrSort) < 0 {
				*fill = append(*fill, rs.newClusterLink(tmpaddr.nodeName, tmpaddr.Addr, traverse))
			}
		}
	}
	// 遍历新配置, 找出哪些是新增的
	fillDialFunc(newCfg, oldCfg, &newDial, selfServer)
	// 遍历老配置, 找出哪些是需要删除的
	fillDialFunc(oldCfg, newCfg, &delDial, selfServer)
	// reload
	rs.cfg = newCfg
	//printFunc := func(cfg []*xhlink.HLinkUnitConfig, tag string) {
	//	xlog.Debugf("print %s Begin!", tag)
	//	for _, info := range cfg {
	//		xlog.Debugf("addr=%s", info.Addr)
	//	}
	//	xlog.Debugf("print %s End!", tag)
	//}
	//printFunc(newDial, "newDial")
	//printFunc(delDial, "delDial")
	return newDial, delDial, nil
}

// 通过Server节点类型找配置中有哪些节点名
func (rs *RPCStatic) GetServerWithPrefix(namePrefix string) []string {
	ret := []string{}
	for nodename := range rs.cfg.Server {
		if strings.HasPrefix(nodename, namePrefix) {
			ret = append(ret, nodename)
		}
	}
	return ret
}

// 获取已经连接的节点类型个数
func (rs *RPCStatic) GetNodeNum(namePrefix string) int {
	num := 0
	for k := range rs.name2link {
		if strings.HasPrefix(k, namePrefix) {
			num++
		}
	}
	return num
}

// 获取已经连接的节点类型具体信息
func (rs *RPCStatic) GetNodes(namePrefix string) []string {
	ret := make([]string, 0)
	for k := range rs.name2link {
		if strings.HasPrefix(k, namePrefix) {
			ret = append(ret, k)
		}
	}
	return ret
}

// 通过节点名获取RemoteID
func (rs *RPCStatic) GetRemoteIDByNodeName(name string) int32 {
	link := rs.name2link[name]
	if link != nil {
		return link.GetRemoteID()
	}
	return 0
}

// ClusterHandler set cluster callback
type ClusterHandler interface {
	OnOpen(node string, link *xnet.Link)
	OnClose(node string, link *xnet.Link)
}

// 添加rpc连接监听handler
func (rs *RPCStatic) AddClusterHandler(namePrefix string, handler ClusterHandler) {
	rs.handler[namePrefix] = handler
}

// 调用rpc连接open事件
func (rs *RPCStatic) callClusterOnOpen(name string, link *xnet.Link) {
	for nodePrefix, h := range rs.handler {
		if strings.HasPrefix(name, nodePrefix) {
			h.OnOpen(name, link)
			break
		}
	}
}

// 调用rpc连接close事件
func (rs *RPCStatic) callClusterOnClose(name string, link *xnet.Link) {
	for nodePrefix, h := range rs.handler {
		if strings.HasPrefix(name, nodePrefix) {
			h.OnClose(name, link)
			break
		}
	}
}
