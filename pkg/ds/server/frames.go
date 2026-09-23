package server

import (
	"math"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/data"

	"local-ecs-app/pkg/aliyun/model"
)

// 帧字段白名单：全部来自 PublicAsset 级已裁剪信息，绝不包含任何 IP/地址字段。
// 守护测试 frames_test.go 会序列化整帧断言无 IP 泄漏。

// assetsFrame 按告警的「宽序列」形态组织：time 列 + 每台实例一个带标签的
// daysToExpire 数值字段（SSE 的 reduce/threshold 只吃这种形态，直接建表会报
// "input data must be a wide series"）。标签（instanceId/名称/规格/地域/计费
// 方式）会随告警实例带出，可直接用于通知路由；按量付费（无固定到期）的字段
// 值为 null，配 dropNN 后不产生告警实例。
func assetsFrame(list []model.Instance, now time.Time) *data.Frame {
	fields := []*data.Field{data.NewField("time", nil, []time.Time{now})}
	for _, inst := range list {
		labels := data.Labels{
			"instanceId": inst.InstanceID,
			"name":       inst.InstanceName,
			"type":       inst.InstanceType,
			"region":     inst.RegionID,
			"chargeType": inst.ChargeType,
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
	return data.NewFrame(string(frameAssets), fields...)
}

// accountFrame 账户概览单行宽表。BSS 整体软失败时 IsZero：保留同一 schema、
// 值全 null（规则评估落到 NoData 状态，而不是帧结构漂移）。
func accountFrame(ov model.AccountOverview, now time.Time) *data.Frame {
	amounts := []*float64{ptr(ov.Available), ptr(ov.Coupon), ptr(ov.BillTotal)}
	if ov.IsZero() {
		amounts = []*float64{nil, nil, nil}
	}
	currency := []string{ov.Currency}
	cycle := []string{ov.BillingCycle}
	return data.NewFrame(
		string(frameAccount),
		data.NewField("time", nil, []time.Time{now}),
		data.NewField("availableAmount", nil, amounts[0:1]),
		data.NewField("couponAmount", nil, amounts[1:2]),
		data.NewField("billTotal", nil, amounts[2:3]),
		data.NewField("currency", nil, currency),
		data.NewField("billingCycle", nil, cycle),
	)
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
