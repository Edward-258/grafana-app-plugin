package model

import "testing"

// 阿里云金额字符串可能带千分位逗号（cloudscope 实测），坏值记 0。
func TestParseMoney(t *testing.T) {
	cases := map[string]float64{
		"7.36":       7.36,
		"150,000.00": 150000,
		"":           0,
		"abc":        0,
		"-3.5":       -3.5,
	}
	for in, want := range cases {
		if got := ParseMoney(in); got != want {
			t.Errorf("ParseMoney(%q)=%v want %v", in, got, want)
		}
	}
}

func TestAccountOverviewIsZero(t *testing.T) {
	empty := AccountOverview{}
	if !empty.IsZero() {
		t.Fatal("空概览应 IsZero")
	}
	if (AccountOverview{Available: 0.01}).IsZero() {
		t.Fatal("有余额不应 IsZero")
	}
	if (AccountOverview{Coupon: 5}).IsZero() {
		t.Fatal("有代金券不应 IsZero")
	}
}
