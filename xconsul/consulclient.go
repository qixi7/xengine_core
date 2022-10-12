package xconsul

import (
	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
	"github.com/qixi7/xengine_core/xlog"
	"time"
)

type HTTPConfig struct {
	HttpAddr string // http地址: http://ip:port
}

type ConsulClient struct {
	*HTTPConfig
	Addr        string
	Port        int
	ServiceID   string
	ServiceName string
	Tags        []string
	Check       *api.AgentServiceCheck
	Checks      api.AgentServiceChecks
	Client      *api.Client
	Cfg         *api.Config
}

// --------------------- func -----------------------

// consul client service check config
func AgentServiceCheckString(check *api.AgentServiceCheck, kv map[string]string) *api.AgentServiceCheck {
	if len(kv) == 0 {
		return nil
	}
	if check == nil {
		check = new(api.AgentServiceCheck)
	}

	for k, v := range kv {
		switch k {
		case "CheckID":
			check.CheckID = v
		case "Name":
			check.Name = v
		case "DockerContainerID":
			check.DockerContainerID = v
		case "Shell":
			check.Shell = v
		case "Interval":
			check.Interval = v
		case "TimeOut":
			check.Timeout = v
		case "TTL":
			check.TTL = v
		case "HTTP":
			check.HTTP = v
		case "TCP":
			check.TCP = v
		case "Status":
			check.Status = v
		case "Notes":
			check.Notes = v
		case "GRPC":
			check.GRPC = v
		case "DeregisterCriticalServiceAfter":
			check.DeregisterCriticalServiceAfter = v
		}
	}

	return check
}

func AgentServiceChecksString(checks ...*api.AgentServiceCheck) api.AgentServiceChecks {
	var res api.AgentServiceChecks
	return append(res, checks...)
}

func (p *ConsulClient) newConfig() *api.Config {
	res := new(api.Config)
	res.WaitTime = time.Second
	res.Address = p.HttpAddr
	p.Cfg = res
	return res
}

func (p *ConsulClient) getConfig() *api.Config {
	if p.Cfg != nil {
		return p.Cfg
	}
	return p.newConfig()
}

func (p *ConsulClient) getClient() (*api.Client, error) {
	if p.Client != nil {
		return p.Client, nil
	}
	cfg := p.getConfig()
	if cfg == nil {
		return nil, errors.New("bad config consul https config!")
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		xlog.Errorf("ConsulClient.newClient, New client error=%v", err)
		return nil, err
	}
	p.Client = client
	return p.Client, nil
}

func (p *ConsulClient) SetServiceID(serviceID string) {
	p.ServiceID = serviceID
}

func (p *ConsulClient) Register() bool {
	client, err := p.getClient()
	if err != nil {
		return false
	}
	check := &api.AgentServiceRegistration{
		ID:      p.ServiceID,
		Name:    p.ServiceName,
		Tags:    p.Tags,
		Address: p.Addr,
		Port:    p.Port,
		Check:   p.Check,
		Checks:  p.Checks,
	}
	err = client.Agent().ServiceRegister(check)
	if err != nil {
		xlog.Error(err)
		return false
	}
	xlog.InfoF("register service %s to %s ok", p.ServiceName, p.getConfig().Address)
	return true
}

// 取消注册
func (p *ConsulClient) DeRegister() {
	client, err := p.getClient()
	if err == nil {
		err = client.Agent().ServiceDeregister(p.ServiceID)
	}
}

// 服务信息
func (p *ConsulClient) CateLogService(service string, tags []string) ([]*api.ServiceEntry, error) {
	client, err := p.getClient()
	if err != nil {
		return nil, err
	}
	serviceData, _, err := client.Health().ServiceMultipleTags(service, tags, true, &api.QueryOptions{})
	if err != nil {
		return nil, err
	}
	return serviceData, nil
}
