package client

import (
	"encoding/json"
	"testing"
)

// 阿里云原始响应 → toInstance：租期三字段解析正确；
// StartTime（开机时间）与租期无关，出现在响应里也应被忽略。
func TestRawInstanceParsesLeaseFields(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		creation   string
		expired    string
		chargeType string
	}{
		{
			name: "包年包月",
			body: `{"InstanceId":"i-1","InstanceName":"web","HostName":"web",
				"InstanceType":"ecs.e-c1m2.large","Cpu":2,"Memory":4096,
				"ZoneId":"cn-heyuan-b","RegionId":"cn-heyuan",
				"CreationTime":"2024-05-20T08:30Z","ExpiredTime":"2026-05-20T08:30Z",
				"InstanceChargeType":"PrePaid","StartTime":"2026-09-01T00:00Z"}`,
			creation:   "2024-05-20T08:30Z",
			expired:    "2026-05-20T08:30Z",
			chargeType: "PrePaid",
		},
		{
			name: "按量付费（无到期字段）",
			body: `{"InstanceId":"i-2","InstanceName":"spot","HostName":"spot",
				"InstanceType":"ecs.e-c1m1.large","Cpu":1,"Memory":2048,
				"RegionId":"cn-hangzhou","CreationTime":"2025-01-02T03:04Z",
				"InstanceChargeType":"PostPaid"}`,
			creation:   "2025-01-02T03:04Z",
			expired:    "",
			chargeType: "PostPaid",
		},
	}
	for _, tc := range cases {
		var raw rawInstance
		if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		inst := raw.toInstance()
		if inst.CreationTime != tc.creation || inst.ExpiredTime != tc.expired || inst.ChargeType != tc.chargeType {
			t.Errorf("%s: 租期字段解析错误 creation=%q expired=%q charge=%q", tc.name, inst.CreationTime, inst.ExpiredTime, inst.ChargeType)
		}
	}
}

// BSS QueryAvailableInstances：字段解析 + ProductCode 客户端二次过滤（服务端筛 ecs 仍混入 sas）。
func TestParseAvailableInstancesFiltersNonEcs(t *testing.T) {
	body := []byte(`{
		"Success": true,
		"Data": {
			"TotalCount": 3,
			"InstanceList": [
				{"ProductCode": "ecs", "InstanceID": "i-1", "CreateTime": "2026-08-14T08:51:03Z"},
				{"ProductCode": "sas", "InstanceID": "sas_x", "CreateTime": "2026-06-23T08:00:00Z"},
				{"ProductCode": "ecs", "InstanceID": "i-2", "CreateTime": "2026-09-14T06:49:48Z"}
			]
		}
	}`)
	var parsed availableInstancesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range parsed.Data.InstanceList {
		if it.ProductCode == "ecs" && it.InstanceID != "" {
			got[it.InstanceID] = it.CreateTime
		}
	}
	if len(got) != 2 || got["i-1"] != "2026-08-14T08:51:03Z" || got["i-2"] != "2026-09-14T06:49:48Z" {
		t.Fatalf("应只保留 ecs 产品: %v", got)
	}
	if _, ok := got["sas_x"]; ok {
		t.Fatalf("非 ecs 产品不应混入: %v", got)
	}
}
