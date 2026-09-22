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
	Code string `json:"Code"`
	Message string `json:"Message"`
	// BSS 系（如 QueryAvailableInstances）的成功响应同样携带 Code/Message
	//（"Success"/"Successful!"），必须以显式 Success 标志区分成败；
	// ECS 系响应不带该字段（nil），维持原"Code+Message 即错误"的判断。
	Success *bool `json:"Success"`
}

// availableInstancesResponse 是 BSS QueryAvailableInstances（已购资源）的响应。
// 只解析本链路关心的字段；ProductCode 需客户端二次过滤（服务端按 ecs 筛
// 仍会混入 sas 等产品，2026-09-23 实测）。
type availableInstancesResponse struct {
	Success bool `json:"Success"`
	Data    struct {
		TotalCount   int `json:"TotalCount"`
		InstanceList []struct {
			ProductCode string `json:"ProductCode"`
			InstanceID  string `json:"InstanceID"`
			CreateTime  string `json:"CreateTime"`
		} `json:"InstanceList"`
	} `json:"Data"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
