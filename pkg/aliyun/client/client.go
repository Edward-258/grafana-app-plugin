package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"local-ecs-app/pkg/aliyun/model"
	"local-ecs-app/pkg/aliyun/rpc"
)

// client 是纯 HTTP 客户端：签名、分页、全地域并发枚举。
// 领域类型与匹配逻辑在 model 包，上层（service）直接引用 model，不经此处转发。

type Client struct {
	cfg  model.Config
	http *http.Client
}

func New(cfg model.Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) endpoint(region string) string {
	return "https://ecs." + region + ".aliyuncs.com/"
}

// bssEndpoint 是费用中心（BSS）OpenAPI 入口；注意不是 bssopenapi.aliyuncs.com
// （该域名已不存在，2026-09-23 实测 NXDOMAIN）。
const bssEndpoint = "https://business.aliyuncs.com/"

// CreationTimes 从 BSS「已购资源」取每台 ECS 的精确创建时间（订单口径，
// 与 ECS CreationTime 可能略有出入）。一次全局调用，无地域枚举；
// Version 经 action map 覆写 call() 里 ECS 的默认值。分页上限与 List 同为 20 页。
func (c *Client) CreationTimes(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	seen := 0 // TotalCount 是全口径（含服务端混入的非 ecs 行），按见过的行数翻页
	for page := 1; page <= 20; page++ {
		body, err := c.call(ctx, bssEndpoint, map[string]string{
			"Action":      "QueryAvailableInstances",
			"Version":     "2017-12-14",
			"ProductCode": "ecs",
			"PageNum":     strconv.Itoa(page),
			"PageSize":    "100",
		})
		if err != nil {
			return nil, err
		}
		var parsed availableInstancesResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("解析 BSS 响应失败: %w", err)
		}
		for _, it := range parsed.Data.InstanceList {
			if it.ProductCode == "ecs" && it.InstanceID != "" {
				out[it.InstanceID] = it.CreateTime
			}
		}
		seen += len(parsed.Data.InstanceList)
		if seen >= parsed.Data.TotalCount || len(parsed.Data.InstanceList) == 0 {
			break
		}
	}
	return out, nil
}

// List paginates DescribeInstances within one region.
// 上限 20 页 × 100 = 单地域最多收集 2000 台，超出会静默截断——轻量内部
// 工具的有意取舍（2026-09 拍板不处理）；若将来单地域逼近千台，把此处
// 改成超限报错以守住"禁止部分结果"红线。
func (c *Client) List(ctx context.Context, region string) ([]model.Instance, error) {
	var all []model.Instance
	for page := 1; page <= 20; page++ {
		batch, total, err := c.describe(ctx, region, page, 100)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(all) >= total || len(batch) == 0 {
			break
		}
	}
	return all, nil
}

func (c *Client) describe(ctx context.Context, region string, page, size int) ([]model.Instance, int, error) {
	body, err := c.call(ctx, c.endpoint(region), map[string]string{
		"Action":     "DescribeInstances",
		"RegionId":   region,
		"PageNumber": strconv.Itoa(page),
		"PageSize":   strconv.Itoa(size),
	})
	if err != nil {
		return nil, 0, err
	}
	var parsed describeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("解析 ECS 响应失败: %w", err)
	}
	out := make([]model.Instance, 0, len(parsed.Instances.Instance))
	for _, raw := range parsed.Instances.Instance {
		out = append(out, raw.toInstance())
	}
	return out, parsed.TotalCount, nil
}

// maxResponseBytes 单个阿里云响应的读取上限。分页 PageSize=100 时单页响应
// 只有几百 KB，4MB 是异常检测线：超过即说明上游行为异常（或未来有人调大
// 分页），拒绝处理而不是把未知体积读进内存。
const maxResponseBytes = 4 << 20

// call signs and posts one RPC request and returns the raw body of a healthy response.
func (c *Client) call(ctx context.Context, host string, action map[string]string) ([]byte, error) {
	params := map[string]string{
		"AccessKeyId":      c.cfg.AccessKeyID,
		"Format":           "JSON",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   strconv.FormatInt(time.Now().UnixNano(), 10),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2014-05-26",
	}
	for k, v := range action {
		params[k] = v
	}
	params["Signature"] = rpc.Sign("POST", params, c.cfg.AccessKeySecret)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("阿里云响应超过 %dMB 上限，已拒绝处理", maxResponseBytes>>20)
	}

	var env apiEnvelope
	_ = json.Unmarshal(body, &env)
	if env.Code != "" && env.Message != "" && (env.Success == nil || !*env.Success) {
		return nil, fmt.Errorf("阿里云 %s: %s", env.Code, env.Message)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("阿里云 HTTP %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}
