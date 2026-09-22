package model

import "errors"

var ErrNoSettings = errors.New("未配置 AccessKey，请到插件配置页填写 AccessKey")

type Config struct {
	AccessKeyID     string
	AccessKeySecret string
}

// Instance is backend-only. IP fields never go to the browser.
type Instance struct {
	InstanceID   string
	InstanceName string
	HostName     string
	InstanceType string
	CPU          int
	MemoryGiB    int
	PrivateIPs   []string
	PublicIPs    []string
	ZoneID       string
	RegionID     string
	// 租期信息：CreationTime 是实例创建时刻（ECS 口径，续费不更新）；
	// ExpiredTime 仅 PrePaid 有值；原始 UTC ISO 字符串原样透传。
	CreationTime string
	ExpiredTime  string
	ChargeType   string
	// LeaseStartTime 是 BSS 订单口径的资源开通时间（租赁开始）；与
	// CreationTime 多数情况相同或差秒级，语义不同故分列展示。
	// BSS 补充链路失败时为空，前端显示占位。
	LeaseStartTime string
}

// PublicAsset is the payload returned to the plugin UI. No addresses.
type PublicAsset struct {
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	HostName     string `json:"hostName"`
	InstanceType string `json:"instanceType"`
	CPU          int    `json:"cpu"`
	MemoryGiB    int    `json:"memoryGiB"`
	ZoneID       string `json:"zoneId,omitempty"`
	RegionID     string `json:"regionId,omitempty"`
	CreationTime string `json:"creationTime,omitempty"`
	ExpiredTime  string `json:"expiredTime,omitempty"`
	ChargeType   string `json:"chargeType,omitempty"`
	LeaseStart   string `json:"leaseStart,omitempty"`
}

func (i Instance) Public() PublicAsset {
	return PublicAsset{
		InstanceID:   i.InstanceID,
		InstanceName: i.InstanceName,
		HostName:     i.HostName,
		InstanceType: i.InstanceType,
		CPU:          i.CPU,
		MemoryGiB:    i.MemoryGiB,
		ZoneID:       i.ZoneID,
		RegionID:     i.RegionID,
		CreationTime:   i.CreationTime,
		ExpiredTime:    i.ExpiredTime,
		ChargeType:     i.ChargeType,
		LeaseStart:     i.LeaseStartTime,
	}
}

// RegionSet returns the distinct regions covered by the list.
func RegionSet(list []Instance) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(list))
	for _, inst := range list {
		if inst.RegionID == "" {
			continue
		}
		if _, ok := seen[inst.RegionID]; ok {
			continue
		}
		seen[inst.RegionID] = struct{}{}
		out = append(out, inst.RegionID)
	}
	return out
}

func Unique(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
