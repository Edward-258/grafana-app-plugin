package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"

	"local-ecs-app/pkg/aliyun/model"
	"local-ecs-app/pkg/app/handler"
	"local-ecs-app/pkg/app/service"
)

func testCred(id string) service.Credential {
	return service.Credential{ID: id, Label: "标签-" + id, AccessKeyID: "ak-" + id, AccessKeySecret: "sk"}
}

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
	snaps := []service.Snapshot{{Cred: testCred("s1"), Instances: list}}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	frames := map[string][]byte{}
	for name, f := range map[string]interface{ MarshalJSON() ([]byte, error) }{
		"assets":  assetsFrame(snaps, now),
		"account": accountFrame([]service.Snapshot{{Cred: testCred("s1"), Billing: model.AccountOverview{Available: 7.36, Currency: "CNY"}}}, now),
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
	if strings.Contains(string(frames["assets"]), "i-post") {
		t.Fatalf("assets 帧不应包含按量付费实例（到期告警只服务包年包月）: %s", frames["assets"])
	}
}

func TestAssetsFrameDaysToExpire(t *testing.T) {
	list := []model.Instance{
		{InstanceID: "i-pre", InstanceName: "pre", ExpiredTime: "2026-10-04T00:00:00Z", ChargeType: "PrePaid"},
		{InstanceID: "i-post", InstanceName: "post", ChargeType: "PostPaid"},
	}
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	f := assetsFrame([]service.Snapshot{{Cred: testCred("s1"), Instances: list}}, now)
	if f.Rows() != 1 {
		t.Fatalf("宽序列应单行，得到 %d 行", f.Rows())
	}
	if f.Fields[0].Name != "time" {
		t.Fatalf("首列应为 time，得到 %s", f.Fields[0].Name)
	}
	// 按量付费被过滤：只剩包年包月实例一个 daysToExpire 字段
	if len(f.Fields) != 2 {
		t.Fatalf("应有 time + 1 个包年包月序列，得到 %d 个字段", len(f.Fields))
	}
	pre := f.Fields[1]
	if pre.Name != "daysToExpire" || pre.Labels["instanceId"] != "i-pre" {
		t.Fatalf("pre 序列异常: %s %v", pre.Name, pre.Labels)
	}
	// 2026-09-24 → 2026-10-04 恰好 10 天
	if got := *pre.At(0).(*float64); got != 10 {
		t.Fatalf("daysToExpire = %v, 期望 10", got)
	}
	if pre.Labels["chargeType"] != "PrePaid" || pre.Labels["name"] != "pre" {
		t.Fatalf("标签缺失: %v", pre.Labels)
	}
	if pre.Labels["ak"] != "s1" || pre.Labels["akLabel"] != "标签-s1" {
		t.Fatalf("序列应带来源 AK 标签: %v", pre.Labels)
	}
}

// 多 AK：assets 帧跨快照合并（同名 daysToExpire 序列靠 labels 区分）；
// 失败 AK 的快照缺席即无序列（评估落 NoData），不产生空壳字段。
func TestAssetsFrameMultiAK(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	snaps := []service.Snapshot{
		{Cred: testCred("s1"), Instances: []model.Instance{
			{InstanceID: "i-a", ExpiredTime: "2026-10-04T00:00:00Z", ChargeType: "PrePaid"},
		}},
		{Cred: testCred("s2"), Instances: []model.Instance{
			{InstanceID: "i-b", ExpiredTime: "2026-11-03T00:00:00Z", ChargeType: "PrePaid"},
		}},
	}
	f := assetsFrame(snaps, now)
	if len(f.Fields) != 3 { // time + 两台实例
		t.Fatalf("两 AK 各出一序列，得到 %d 个字段", len(f.Fields))
	}
	if f.Fields[1].Labels["ak"] != "s1" || f.Fields[2].Labels["ak"] != "s2" {
		t.Fatalf("序列 ak 标签不符: %v %v", f.Fields[1].Labels, f.Fields[2].Labels)
	}
	if got := *f.Fields[2].At(0).(*float64); got != 40 {
		t.Fatalf("第二台 daysToExpire = %v, 期望 40", got)
	}
	// 失败 AK 缺席：快照列表里没有它就没有它的序列
	f2 := assetsFrame(snaps[:1], now)
	if len(f2.Fields) != 2 {
		t.Fatalf("缺席 AK 不应产生字段: %d", len(f2.Fields))
	}
}

func TestAccountFrameSchemaStable(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	cred := testCred("s1")
	ppb := 107.93
	zeroSnap := service.Snapshot{Cred: cred, Instances: []model.Instance{{InstanceID: "i-post", ChargeType: "PostPaid"}}}
	empty := accountFrame([]service.Snapshot{zeroSnap}, now)
	full := accountFrame([]service.Snapshot{{
		Cred:      cred,
		Instances: []model.Instance{{InstanceID: "i-post", ChargeType: "PostPaid"}},
		Billing:   model.AccountOverview{Available: 7.36, Coupon: 1, BillTotal: 108.14, Currency: "CNY", BillingCycle: "2026-09", BillByInstance: map[string]float64{"i-post": ppb}},
	}}, now)
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
	// billTotal 独立于 ov.BillTotal：只统计按量付费实例的当月实付
	if got := *full.Fields[3].At(0).(*float64); got != 107.93 {
		t.Fatalf("billTotal = %v, 期望按量付费实付 107.93", got)
	}
	if full.Fields[3].Labels["chargeType"] != "PostPaid" {
		t.Fatalf("billTotal 缺 chargeType 标签: %v", full.Fields[3].Labels)
	}
	// metric+ak 标签区分序列：Grafana 要求告警实例标签集唯一，缺它会因
	// labels 撞车整体拒绝；多 AK 时 ak 是唯一性保证
	for i, want := range []string{"availableAmount", "couponAmount", "billTotal"} {
		if got := full.Fields[i+1].Labels["metric"]; got != want {
			t.Fatalf("字段 %d metric 标签 = %q, 期望 %q", i+1, got, want)
		}
		if got := full.Fields[i+1].Labels["ak"]; got != "s1" {
			t.Fatalf("字段 %d ak 标签 = %q, 期望 s1", i+1, got)
		}
	}
	if full.Fields[1].Labels["currency"] != "CNY" || full.Fields[1].Labels["billingCycle"] != "2026-09" {
		t.Fatalf("币种/账期应走字段标签: %v", full.Fields[1].Labels)
	}
	if !empty.NilAt(1, 0) {
		t.Fatal("零值概览应全 null（规则评估落到 NoData）")
	}
	// 实例账单不可用（BSS 软失败）：billTotal 为 null，绝不发 0 冒充
	partial := accountFrame([]service.Snapshot{{
		Cred:    cred,
		Billing: model.AccountOverview{Available: 7.36},
	}}, now)
	if !partial.NilAt(3, 0) {
		t.Fatal("实例账单缺失时 billTotal 应为 null")
	}
	if partial.NilAt(1, 0) {
		t.Fatal("实例账单缺失不影响余额字段")
	}
}

// 多 AK 的 account 帧：每个 AK 一组三字段，标签集靠 ak 唯一（跨 AK 标签撞车
// 会被告警引擎整体拒绝——AGENTS.md 台账 14④b 的多 AK 版）。
func TestAccountFrameMultiAK(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	snaps := []service.Snapshot{
		{Cred: testCred("s1"), Billing: model.AccountOverview{Available: 1.1, Currency: "CNY", BillingCycle: "2026-09"}},
		{Cred: testCred("s2"), Billing: model.AccountOverview{Available: 2.2, Currency: "CNY", BillingCycle: "2026-09"}},
	}
	f := accountFrame(snaps, now)
	if f.Rows() != 1 {
		t.Fatalf("account 帧应单行宽序列，得到 %d 行", f.Rows())
	}
	if len(f.Fields) != 1+3*2 {
		t.Fatalf("两 AK 应各出三字段: %d", len(f.Fields))
	}
	seen := map[string]bool{}
	for _, field := range f.Fields[1:] {
		ak := field.Labels["ak"]
		if ak != "s1" && ak != "s2" {
			t.Fatalf("ak 标签异常: %v", field.Labels)
		}
		key := ak + "/" + field.Labels["metric"]
		if seen[key] {
			t.Fatalf("序列标签集撞车: %s", key)
		}
		seen[key] = true
	}
	if got := *f.Fields[4].At(0).(*float64); got != 2.2 {
		t.Fatalf("第二个 AK 的 availableAmount = %v, 期望 2.2", got)
	}
}

func TestPostPaidBill(t *testing.T) {
	list := []model.Instance{
		{InstanceID: "i-pre", ChargeType: "PrePaid"},
		{InstanceID: "i-post1", ChargeType: "PostPaid"},
		{InstanceID: "i-post2", ChargeType: "PostPaid"},
		{InstanceID: "i-other", ChargeType: "PostPaid"},
	}
	bills := map[string]float64{"i-pre": 5, "i-post1": 107.93, "i-post2": 0.07, "i-unknown": 9}
	if got := *postPaidBill(list, bills); got != 108 {
		t.Fatalf("按量付费实付合计 = %v, 期望 108（不含包年包月与快照外实例）", got)
	}
	if postPaidBill(list, nil) != nil {
		t.Fatal("实例账单不可用应返回 nil（billTotal 置 null）")
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

// settings 多键读取：akList 插槽 + ak:<slot>:* 键；sync 未跑完的窗口期回退
// legacy 单键；全空 ErrNoSettings。
func TestDSSettings(t *testing.T) {
	pCtx := func(jsonData string, secure map[string]string) backend.PluginContext {
		return backend.PluginContext{DataSourceInstanceSettings: &backend.DataSourceInstanceSettings{
			JSONData:                []byte(jsonData),
			DecryptedSecureJSONData: secure,
		}}
	}
	creds, err := settings(pCtx(
		`{"akList":[{"slot":"s1","label":"主账号"},{"slot":"s2"}]}`,
		map[string]string{
			handler.SecureKeyID("s1"): "LTAI1", handler.SecureKeySecret("s1"): "sk1",
			handler.SecureKeyID("s2"): "LTAI2", // secret 缺 → 跳过
		},
	))
	if err != nil || len(creds) != 1 || creds[0].ID != "s1" || creds[0].Label != "主账号" || creds[0].AccessKeyID != "LTAI1" {
		t.Fatalf("多插槽解析不符: %+v err=%v", creds, err)
	}

	legacy, err := settings(pCtx("", map[string]string{"accessKeyId": "LTAI-old", "accessKeySecret": "sk-old"}))
	if err != nil || len(legacy) != 1 || legacy[0].ID != service.LegacyCredentialID || legacy[0].AccessKeyID != "LTAI-old" {
		t.Fatalf("legacy 回退不符: %+v err=%v", legacy, err)
	}

	if _, err := settings(pCtx("", nil)); !errors.Is(err, service.ErrNoSettings) {
		t.Fatalf("全空应 ErrNoSettings, got %v", err)
	}
	if _, err := settings(backend.PluginContext{}); !errors.Is(err, service.ErrNoSettings) {
		t.Fatalf("无 ds 设置应 ErrNoSettings, got %v", err)
	}
}
