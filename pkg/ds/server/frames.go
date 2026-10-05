package server

import (
	"math"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"

	"local-ecs-app/pkg/app/service"
	"local-ecs-app/pkg/aliyun/model"
)

// 帧字段白名单：全部来自 PublicAsset 级已裁剪信息，绝不包含任何 IP/地址字段。
// 守护测试 frames_test.go 会序列化整帧断言无 IP 泄漏。

// assetsFrame 服务**包年包月（PrePaid）**实例：每台一个带标签的
// daysToExpire 数值字段（SSE 要求宽序列：time 列 + 数值列，多行宽表会被判
// long 拒收）。按量付费没有到期概念，不输出 null 序列制造噪音（用户拍板，
// 见 AGENTS.md 台账 14）。labels 随告警实例带出，可直接用于通知路由；ak/akLabel
// 标注来源凭证，多 AK 时供告警规则按账号路由。
func assetsFrame(snaps []service.Snapshot, now time.Time) *data.Frame {
	fields := []*data.Field{data.NewField("time", nil, []time.Time{now})}
	for _, s := range snaps {
		for _, inst := range s.Instances {
			if inst.ChargeType != "PrePaid" {
				continue
			}
			labels := data.Labels{
				"instanceId": inst.InstanceID,
				"name":       inst.InstanceName,
				"type":       inst.InstanceType,
				"region":     inst.RegionID,
				"chargeType": inst.ChargeType,
				"ak":         s.Cred.ID,
			}
			if s.Cred.Label != "" {
				labels["akLabel"] = s.Cred.Label
			}
			for k, v := range labels {
				if v == "" {
					delete(labels, k) // 空值标签去掉，避免噪声标签
				}
			}
			var days *float64
			if t := parseTime(inst.ExpiredTime); t != nil {
				d := math.Floor(t.Sub(now).Hours() / 24)
				days = &d
			}
			fields = append(fields, data.NewField("daysToExpire", labels, []*float64{days}))
		}
	}
	return data.NewFrame(string(frameAssets), fields...)
}

// accountFrame 账户概览单行宽序列，每个 AK 一组三字段（同字段名多序列靠
// labels 区分——assets 帧已有多条同名 daysToExpire 序列的先例；ak 标签保证
// 跨 AK 标签集唯一，SSE 要求标签能唯一定位序列）。availableAmount/
// couponAmount 是该账户的余额池（按量付费实例的消耗来源，无法按实例拆分）；
// billTotal 只统计**该账户下按量付费实例**的当月实付（postPaidBill 由调用方
// 从实例快照 × 实例级账单求和，nil 表示实例账单不可用）。附加信息一律走字段
// labels：独立字符串列会把整帧判成 long 形态被 SSE 拒收。BSS 整体软失败的
// AK：保留同一 schema、值全 null（规则评估落到 NoData 状态，而不是帧结构
// 漂移）；快照整体缺席（拉取失败被剔除）的 AK 同理不出现字段。
func accountFrame(snaps []service.Snapshot, now time.Time) *data.Frame {
	fields := []*data.Field{data.NewField("time", nil, []time.Time{now})}
	for _, s := range snaps {
		bill := postPaidBill(s.Instances, s.Billing.BillByInstance)
		avail, coupon := ptr(s.Billing.Available), ptr(s.Billing.Coupon)
		if s.Billing.IsZero() {
			avail, coupon, bill = nil, nil, nil
		}
		labels := func(metric string) data.Labels {
			l := data.Labels{"metric": metric, "ak": s.Cred.ID}
			if s.Cred.Label != "" {
				l["akLabel"] = s.Cred.Label
			}
			if s.Billing.Currency != "" {
				l["currency"] = s.Billing.Currency
			}
			if s.Billing.BillingCycle != "" {
				l["billingCycle"] = s.Billing.BillingCycle
			}
			return l
		}
		billLabels := labels("billTotal")
		billLabels["chargeType"] = "PostPaid"
		fields = append(fields,
			data.NewField("availableAmount", labels("availableAmount"), []*float64{avail}),
			data.NewField("couponAmount", labels("couponAmount"), []*float64{coupon}),
			data.NewField("billTotal", billLabels, []*float64{bill}),
		)
	}
	return data.NewFrame(string(frameAccount), fields...)
}

// postPaidBill 汇总按量付费实例的当月实付（实例级账单 × 快照计费方式求和）。
// 实例账单不可用（BSS 软失败）时返回 nil → billTotal 为 null → 规则评估落
// NoData，绝不发 0 冒充真实账单。
func postPaidBill(instances []model.Instance, bills map[string]float64) *float64 {
	if bills == nil {
		return nil
	}
	total := 0.0
	for _, inst := range instances {
		if inst.ChargeType == "PostPaid" {
			total += bills[inst.InstanceID]
		}
	}
	return &total
}

func ptr[T any](v T) *T { return &v }

// parseTime 兼容两种来源：ECS DescribeInstances 的分钟精度 UTC ISO
// （2026-09-23T07:59Z，见前端 fmtTime 注释）与 BSS 可能的空格分隔格式。
// 解析失败返回 nil（帧里该格为 null）。
func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}
