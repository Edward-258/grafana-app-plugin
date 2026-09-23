package model

import (
	"encoding/json"
	"strconv"
	"strings"
)

// AccountOverview 是 BSS 账户级概览（余额/代金券/当月账单聚合），随资产快照
// 一起缓存。金额统一解析为 float（阿里云金额字符串可能带千分位逗号）。
type AccountOverview struct {
	Available float64 `json:"available"`
	Cash      float64 `json:"cash,omitempty"`
	Credit    float64 `json:"credit,omitempty"`
	Coupon    float64 `json:"coupon,omitempty"`
	Currency  string  `json:"currency,omitempty"`
	// 当月实例账单（QueryInstanceBill，实付口径），按产品聚合
	BillingCycle string     `json:"billingCycle,omitempty"`
	BillTotal    float64    `json:"billTotal,omitempty"`
	BillItems    []BillItem `json:"billItems,omitempty"`
}

// BillItem 是按产品聚合的月账单项。
type BillItem struct {
	Product string  `json:"product"`
	Amount  float64 `json:"amount"`
}

// IsZero 判断概览是否完全无数据（BSS 整体软失败时用于省略下发）。
func (o AccountOverview) IsZero() bool {
	return o.Available == 0 && o.Coupon == 0 && o.BillTotal == 0 && len(o.BillItems) == 0
}

// ParseMoney 解析阿里云金额字符串：千分位逗号（"150,000.00"）剥离，
// 坏值/空值记 0 而非报错——金额是补充数据，不值得为它打断主链路。
func ParseMoney(v string) float64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	if err != nil {
		return 0
	}
	return f
}

// ParseMoneyAny 兼容 BSS 各接口金额形态不一：QueryAccountBalance 返回字符串
// （"7.36"），QueryInstanceBill 的 PretaxAmount 返回裸数字（108.13）。
func ParseMoneyAny(v any) float64 {
	switch t := v.(type) {
	case string:
		return ParseMoney(t)
	case float64:
		return t
	case json.Number:
		f, _ := t.Float64()
		return f
	case int64:
		return float64(t)
	default:
		return 0
	}
}
