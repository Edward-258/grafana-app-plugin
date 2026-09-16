package client

import (
	"fmt"

	"local-ecs-app/pkg/aliyun/model"
)

type describeResponse struct {
	Code       string `json:"Code"`
	Message    string `json:"Message"`
	TotalCount int    `json:"TotalCount"`
	Instances  struct {
		Instance []rawInstance `json:"Instance"`
	} `json:"Instances"`
}

type rawInstance struct {
	InstanceId      string `json:"InstanceId"`
	InstanceName    string `json:"InstanceName"`
	HostName        string `json:"HostName"`
	InstanceType    string `json:"InstanceType"`
	Cpu             int    `json:"Cpu"`
	Memory          int    `json:"Memory"`
	ZoneId          string `json:"ZoneId"`
	RegionId        string `json:"RegionId"`
	InnerIpAddress  ipBag  `json:"InnerIpAddress"`
	PublicIpAddress ipBag  `json:"PublicIpAddress"`
	VpcAttributes   vpcBag `json:"VpcAttributes"`
	EipAddress      struct {
		IpAddress string `json:"IpAddress"`
	} `json:"EipAddress"`
}

type ipBag struct {
	IpAddress []string `json:"IpAddress"`
}

type vpcBag struct {
	PrivateIpAddress ipBag `json:"PrivateIpAddress"`
}

func (r rawInstance) toInstance() model.Instance {
	priv := model.Unique(append(append([]string{}, r.VpcAttributes.PrivateIpAddress.IpAddress...), r.InnerIpAddress.IpAddress...))
	pub := model.Unique(append(append([]string{}, r.PublicIpAddress.IpAddress...), r.EipAddress.IpAddress))
	mem := r.Memory / 1024
	if mem == 0 && r.Memory > 0 {
		mem = 1
	}
	return model.Instance{
		InstanceID:   r.InstanceId,
		InstanceName: r.InstanceName,
		HostName:     r.HostName,
		InstanceType: r.InstanceType,
		CPU:          r.Cpu,
		MemoryGiB:    mem,
		PrivateIPs:   priv,
		PublicIPs:    pub,
		ZoneID:       r.ZoneId,
		RegionID:     r.RegionId,
	}
}

func apiError(parsed describeResponse, status int, body []byte) error {
	if parsed.Code != "" && parsed.Message != "" {
		return fmt.Errorf("阿里云 %s: %s", parsed.Code, parsed.Message)
	}
	if status >= 400 {
		return fmt.Errorf("阿里云 HTTP %d: %s", status, truncate(string(body), 300))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
