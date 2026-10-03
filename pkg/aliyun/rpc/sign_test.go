package rpc

import "testing"

// encode 实现阿里云 POP 规范化编码，与 Go 标准库行为的三处差异都必须锁死：
// 空格是 %20 不是 +（BSS 时间参数 yyyy-MM-dd HH:mm:ss 含空格，退回
// QueryEscape 原样会在接入时间参数时炸出 SignatureDoesNotMatch）；字面 +
// 先转 %2B，不被空格替换误伤；* 与 ~ 按 RFC3986 落位。
func TestEncodeAliyunRules(t *testing.T) {
	cases := map[string]string{
		"2026-09-30 23:59:59": "2026-09-30%2023%3A59%3A59",
		"a+b":                 "a%2Bb",
		"a b*c~d":             "a%20b%2Ac~d",
		"abc123":              "abc123",
		"":                    "",
	}
	for in, want := range cases {
		if got := encode(in); got != want {
			t.Errorf("encode(%q)=%q want %q", in, got, want)
		}
	}
}

// TestSignGolden 已知答案锚点：规范化串
// Action=QueryAvailableInstances&EndTime=2026-09-30%2023%3A59%3A59&Version=2017-12-14
// （注意空格 %20）经 stringToSign=POST&%2F&<二次编码>（%20 变 %2520）后的
// HMAC-SHA1。签名只管规范化层，请求体 form.Encode 产 + 属表单传输惯例，
// 网关解码后才参与比对——两层各归各，见 spec §13①。
func TestSignGolden(t *testing.T) {
	got := Sign("POST", map[string]string{
		"Action":  "QueryAvailableInstances",
		"EndTime": "2026-09-30 23:59:59",
		"Version": "2017-12-14",
	}, "testSecret")
	want := "SZbYvNwWUTv/6AmlsfgQ1S22lEM="
	if got != want {
		t.Errorf("Sign()=%q want %q", got, want)
	}
}
