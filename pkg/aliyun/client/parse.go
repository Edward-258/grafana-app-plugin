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

// accountBalanceResponse 是 BSS QueryAccountBalance（账户余额）的响应。
// 按量付费资源没有实例级额度，消耗的就是账户可用余额（现金+信用）。
type accountBalanceResponse struct {
	Success bool `json:"Success"`
	Data    struct {
		AvailableAmount     string `json:"AvailableAmount"`
		AvailableCashAmount string `json:"AvailableCashAmount"`
		CreditAmount        string `json:"CreditAmount"`
		Currency            string `json:"Currency"`
	} `json:"Data"`
}

// cashCouponsResponse 是 BSS QueryCashCoupons（代金券）的响应，取有效券余额合计。
type cashCouponsResponse struct {
	Success bool `json:"Success"`
	Data    struct {
		CashCoupon []struct {
			Balance string `json:"Balance"`
		} `json:"CashCoupon"`
	} `json:"Data"`
}

// instanceBillResponse 是 BSS QueryInstanceBill（实例账单）的响应。
// 注意 PretaxAmount 是裸数字（与 QueryAccountBalance 的字符串金额不同型）。
// Item 粒度为实例级，InstanceID 可与 ECS 快照的计费方式做关联过滤。
type instanceBillResponse struct {
	Success bool `json:"Success"`
	Data    struct {
		TotalCount int `json:"TotalCount"`
		Items      struct {
			Item []struct {
				InstanceID   string `json:"InstanceID"`
				PipCode      string `json:"PipCode"`
				ProductName  string `json:"ProductName"`
				PretaxAmount any    `json:"PretaxAmount"`
			} `json:"Item"`
		} `json:"Items"`
	} `json:"Data"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
