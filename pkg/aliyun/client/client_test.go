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

// ramScopeServer 起一个假阿里云：/regions 出地域列表，其余路径按地域名
// 分发（good 正常实例、denied 回 Forbidden.RAM、broken 回其它错误）。
func ramScopeServer(t *testing.T, regions string, behavior map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/regions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Regions":{"Region":[` + regions + `]}}`))
	})
	for region, kind := range behavior {
		mux.HandleFunc("/"+region+"/", func(w http.ResponseWriter, _ *http.Request) {
			switch kind {
			case "denied":
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"Code":"Forbidden.RAM","Message":"User not authorized to operate on the specified resource, or this API doesn't support RAM.","RequestId":"t"}`))
			case "broken":
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"Code":"InternalError","Message":"boom","RequestId":"t"}`))
			default:
				_, _ = w.Write([]byte(`{"TotalCount":1,"Instances":{"Instance":[{"InstanceId":"i-scope-1"}]}}`))
			}
		})
	}
	return httptest.NewServer(mux)
}

func scopedClient(srv *httptest.Server) *Client {
	c := New(model.Config{AccessKeyID: "test", AccessKeySecret: "test"})
	c.regionsHost = srv.URL + "/regions"
	c.ecsHostTmpl = srv.URL + "/%s/"
	return c
}

// RAM 收束到部分实例的 AK：被拒地域是可见边界，不应致命（红线3 唯一例外）。
func TestListAllSkipsRamDeniedRegions(t *testing.T) {
	srv := ramScopeServer(t,
		`{"RegionId":"cn-good"},{"RegionId":"cn-denied"}`,
		map[string]string{"cn-good": "ok", "cn-denied": "denied"})
	defer srv.Close()

	list, err := scopedClient(srv).ListAll(context.Background())
	if err != nil {
		t.Fatalf("收束 AK 的枚举不应报错: %v", err)
	}
	if len(list) != 1 || list[0].InstanceID != "i-scope-1" {
		t.Fatalf("应只含授权地域的实例, got %+v", list)
	}
}

// 授权策略完全没覆盖 DescribeInstances（或收束未命中任何地域）→ 仍整体报错。
func TestListAllAllRegionsDeniedFails(t *testing.T) {
	srv := ramScopeServer(t,
		`{"RegionId":"cn-denied-a"},{"RegionId":"cn-denied-b"}`,
		map[string]string{"cn-denied-a": "denied", "cn-denied-b": "denied"})
	defer srv.Close()

	if _, err := scopedClient(srv).ListAll(context.Background()); err == nil || !strings.Contains(err.Error(), "均被 RAM 拒绝") {
		t.Fatalf("全部地域被拒应报错, got %v", err)
	}
}

// 非 RAM 错误（网络/服务端故障）仍致命：跳过逻辑不得扩大到其它错误码。
func TestListAllOtherRegionErrorStillFatal(t *testing.T) {
	srv := ramScopeServer(t,
		`{"RegionId":"cn-good"},{"RegionId":"cn-denied"},{"RegionId":"cn-broken"}`,
		map[string]string{"cn-good": "ok", "cn-denied": "denied", "cn-broken": "broken"})
	defer srv.Close()

	if _, err := scopedClient(srv).ListAll(context.Background()); err == nil || !strings.Contains(err.Error(), "全地域枚举不完整") {
		t.Fatalf("其它错误应保持致命, got %v", err)
	}
}
