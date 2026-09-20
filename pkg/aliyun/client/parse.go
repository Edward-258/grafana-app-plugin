package client

import (
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
	CreationTime    string `json:"CreationTime"`
	ExpiredTime     string `json:"ExpiredTime"`
	ChargeType      string `json:"InstanceChargeType"`
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
		// 时间保持阿里云原始格式（yyyy-MM-ddTHH:mmZ，UTC 分钟精度）透传，
		// 解析和本地时区展示留给前端；StartTime（开机时间）与租期无关，不采。
		CreationTime: r.CreationTime,
		ExpiredTime:  r.ExpiredTime,
		ChargeType:   r.ChargeType,
	}
}

type apiEnvelope struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
