package model

import "errors"

var ErrNoSettings = errors.New("未配置 AccessKey，请到插件配置页填写 Region / AccessKey")

type Config struct {
	Region          string
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
	}
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
