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
