package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"local-ecs-app/pkg/aliyun/model"
)

func TestAuthorize(t *testing.T) {
	cases := []struct {
		name  string
		kind  frameKind
		role  string
		user  bool
		allow bool
	}{
		{"评估态无用户放行 account", frameAccount, "", false, true},
		{"评估态无用户放行 assets", frameAssets, "", false, true},
		{"Viewer 可读 assets", frameAssets, "Viewer", true, true},
		{"Editor 可读 assets", frameAssets, "Editor", true, true},
		{"Admin 可读 assets", frameAssets, "Admin", true, true},
		{"Viewer 不可读 account", frameAccount, "Viewer", true, false},
		{"Editor 可读 account", frameAccount, "Editor", true, true},
		{"Admin 可读 account", frameAccount, "Admin", true, true},
		{"未知角色 fail-closed", frameAccount, "OrgAuditor", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var u *backend.User
			if c.user {
				u = &backend.User{Role: c.role}
			}
			err := authorize(c.kind, u)
			if c.allow && err != nil {
				t.Fatalf("应放行，却拒绝: %v", err)
			}
			if !c.allow && err == nil {
				t.Fatal("应拒绝，却放行")
			}
		})
	}
}

// 红线复检：帧是新的下发面，序列化后的完整 JSON 里不得出现任何 IP。
func TestFramesOmitIPs(t *testing.T) {
	list := []model.Instance{
		{
			InstanceID: "i-abc", InstanceName: "web", HostName: "web-host",
			InstanceType: "ecs.g7.large", CPU: 2, MemoryGiB: 8,
			PrivateIPs: []string{"10.0.0.8"}, PublicIPs: []string{"203.0.113.9"},
			ZoneID: "cn-beijing-a", RegionID: "cn-beijing",
			ExpiredTime: "2027-01-02T03:04:05Z", ChargeType: "PrePaid",
		},
		{
			InstanceID: "i-post", InstanceName: "post", InstanceType: "ecs.g7.large",
			PrivateIPs: []string{"172.16.0.9"}, ChargeType: "PostPaid",
		},
	}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	frames := map[string][]byte{}
	for name, f := range map[string]interface{ MarshalJSON() ([]byte, error) }{
		"assets":  assetsFrame(list, now),
		"account": accountFrame(model.AccountOverview{Available: 7.36, Currency: "CNY"}, now),
	} {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		frames[name] = b
		s := string(b)
		for _, ip := range []string{"10.0.0.8", "203.0.113.9", "172.16.0.9", "privateIp", "publicIp"} {
			if strings.Contains(s, ip) {
				t.Fatalf("%s 帧泄漏 %q: %s", name, ip, s)
			}
		}
	}
	if !strings.Contains(string(frames["assets"]), "i-abc") {
		t.Fatalf("assets 帧缺资产字段: %s", frames["assets"])
	}
}

func TestAssetsFrameDaysToExpire(t *testing.T) {
	list := []model.Instance{
		{InstanceID: "i-pre", InstanceName: "pre", ExpiredTime: "2026-10-04T00:00:00Z", ChargeType: "PrePaid"},
		{InstanceID: "i-post", InstanceName: "post", ChargeType: "PostPaid"},
	}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	f := assetsFrame(list, now)
	if f.Rows() != 1 {
		t.Fatalf("宽序列应单行，得到 %d 行", f.Rows())
	}
	if f.Fields[0].Name != "time" {
		t.Fatalf("首列应为 time，得到 %s", f.Fields[0].Name)
	}
	// 每实例一个 daysToExpire 字段，顺序与列表一致（time 后依次排列）
	if len(f.Fields) != 3 {
		t.Fatalf("应有 time + 2 个实例序列，得到 %d 个字段", len(f.Fields))
	}
	pre, post := f.Fields[1], f.Fields[2]
	if pre.Name != "daysToExpire" || pre.Labels["instanceId"] != "i-pre" {
		t.Fatalf("pre 序列异常: %s %v", pre.Name, pre.Labels)
	}
	if post.Labels["instanceId"] != "i-post" {
		t.Fatalf("post 序列异常: %v", post.Labels)
	}
	// 2026-09-24 → 2026-10-04 恰好 10 天
	if got := *pre.At(0).(*float64); got != 10 {
		t.Fatalf("daysToExpire = %v, 期望 10", got)
	}
	if !f.NilAt(2, 0) {
		t.Fatal("PostPaid 的 daysToExpire 应为 null")
	}
	if pre.Labels["chargeType"] != "PrePaid" || pre.Labels["name"] != "pre" {
		t.Fatalf("标签缺失: %v", pre.Labels)
	}
}

func TestAccountFrameSchemaStable(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	empty := accountFrame(model.AccountOverview{}, now)
	full := accountFrame(model.AccountOverview{Available: 7.36, Coupon: 1, BillTotal: 108.14, Currency: "CNY", BillingCycle: "2026-09"}, now)
	if len(empty.Fields) != len(full.Fields) {
		t.Fatalf("零值与有值时字段数不一致: %d vs %d", len(empty.Fields), len(full.Fields))
	}
	for i := range empty.Fields {
		if empty.Fields[i].Name != full.Fields[i].Name {
			t.Fatalf("字段序漂移：%s vs %s", empty.Fields[i].Name, full.Fields[i].Name)
		}
	}
	// 回归守护：帧里混入字符串列会被 Grafana 判成 long 形态，SSE 直接拒收
	//（"input data must be a wide series but got type long"），只允许 time+数值。
	for _, f := range full.Fields {
		ft := f.Type()
		numeric := false
		for _, nt := range data.NumericFieldTypes() {
			if ft == nt {
				numeric = true
				break
			}
		}
		if ft != data.FieldTypeTime && !numeric {
			t.Fatalf("account 帧混入非数值列 %s (%v)，告警引擎会拒收", f.Name, ft)
		}
	}
	if got := *full.Fields[1].At(0).(*float64); got != 7.36 {
		t.Fatalf("availableAmount = %v", got)
	}
	// metric 标签区分三条序列：Grafana 要求告警实例标签集唯一，缺它会因
	// labels 撞车整体拒绝
	for i, want := range []string{"availableAmount", "couponAmount", "billTotal"} {
		if got := full.Fields[i+1].Labels["metric"]; got != want {
			t.Fatalf("字段 %d metric 标签 = %q, 期望 %q", i+1, got, want)
		}
	}
	if full.Fields[1].Labels["currency"] != "CNY" || full.Fields[1].Labels["billingCycle"] != "2026-09" {
		t.Fatalf("币种/账期应走字段标签: %v", full.Fields[1].Labels)
	}
	if !empty.NilAt(1, 0) {
		t.Fatal("零值概览应全 null（规则评估落到 NoData）")
	}
	if len(empty.Fields[1].Labels) != 1 {
		t.Fatal("空概览应只剩 metric 标签（币种/账期为空不产生标签）")
	}
}

func TestParseFrame(t *testing.T) {
	if parseFrame("") != frameAssets {
		t.Fatal("空值应默认 assets")
	}
	if parseFrame("account") != frameAccount {
		t.Fatal("account 识别失败")
	}
	if parseFrame("bogus") != frameAssets {
		t.Fatal("未知值应回落 assets")
	}
}

func TestParseTime(t *testing.T) {
	if parseTime("") != nil {
		t.Fatal("空串应为 nil")
	}
	if parseTime("garbage") != nil {
		t.Fatal("坏值应为 nil")
	}
	if parseTime("2026-09-23T07:59:59Z") == nil {
		t.Fatal("RFC3339 应可解析")
	}
	if parseTime("2026-09-23T07:59Z") == nil {
		t.Fatal("分钟精度 ISO（ECS 口径）应可解析")
	}
	if parseTime("2026-09-23 07:59:59") == nil {
		t.Fatal("BSS 空格格式应可解析")
	}
}
