package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-ecs-app/pkg/aliyun/model"
)

// call 的 host 是参数，可用 httptest 服务器直接驱动出口闸。
func TestCallResponseSizeCap(t *testing.T) {
	c := New(model.Config{AccessKeyID: "test", AccessKeySecret: "test"})

	// 正常体积响应放行
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"OK": true}`))
	}))
	defer ok.Close()
	if _, err := c.call(context.Background(), ok.URL, map[string]string{"Action": "X"}); err != nil {
		t.Fatalf("正常响应不应报错: %v", err)
	}

	// 超过 4MB 上限 → 明确报错，而不是把未知体积读进内存
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer huge.Close()
	if _, err := c.call(context.Background(), huge.URL, map[string]string{"Action": "X"}); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("超限响应应报错, got %v", err)
	}
}
