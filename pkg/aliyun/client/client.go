package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
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
	// 入口可覆写：单测用 httptest 服务器驱动 ListAll 的全地域扇出
	//（DescribeRegions 入口 + 地域端点模板，%s=region）。
	regionsHost string
	ecsHostTmpl string
}

func New(cfg model.Config) *Client {
	return &Client{
		cfg:         cfg,
		http:        &http.Client{Timeout: 20 * time.Second},
		regionsHost: regionsHost,
		ecsHostTmpl: "https://ecs.%s.aliyuncs.com/",
	}
}

func (c *Client) endpoint(region string) string {
	return fmt.Sprintf(c.ecsHostTmpl, region)
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

// AccountBalance 取账户余额概览（可用/现金/信用，来自 QueryAccountBalance）
// 并叠加有效代金券余额（QueryCashCoupons）——代金券查询软失败记 0，
// 不影响余额主数据（cloudscope 同款取舍）。
func (c *Client) AccountBalance(ctx context.Context) (model.AccountOverview, error) {
	var ov model.AccountOverview
	body, err := c.call(ctx, bssEndpoint, map[string]string{
		"Action":  "QueryAccountBalance",
		"Version": "2017-12-14",
	})
	if err != nil {
		return ov, err
	}
	var bal accountBalanceResponse
	if err := json.Unmarshal(body, &bal); err != nil {
		return ov, fmt.Errorf("解析 BSS 余额响应失败: %w", err)
	}
	ov.Available = model.ParseMoney(bal.Data.AvailableAmount)
	ov.Cash = model.ParseMoney(bal.Data.AvailableCashAmount)
	ov.Credit = model.ParseMoney(bal.Data.CreditAmount)
	ov.Currency = bal.Data.Currency

	if cbody, err := c.call(ctx, bssEndpoint, map[string]string{
		"Action":         "QueryCashCoupons",
		"Version":        "2017-12-14",
		"EffectiveOrNot": "true",
	}); err == nil {
		var cp cashCouponsResponse
		if json.Unmarshal(cbody, &cp) == nil {
			for _, coupon := range cp.Data.CashCoupon {
				ov.Coupon += model.ParseMoney(coupon.Balance)
			}
		}
	}
	return ov, nil
}

// MonthlyBill 按 cycle（YYYY-MM）聚合实例账单（QueryInstanceBill，实付口径），
// 按产品分组求和、金额降序。分页 PageSize=300、上限 20 页与 List 的取舍一致。
func (c *Client) MonthlyBill(ctx context.Context, cycle string) ([]model.BillItem, error) {
	agg := map[string]float64{}
	seen := 0 // TotalCount 为全口径，按见过的行数翻页
	for page := 1; page <= 20; page++ {
		body, err := c.call(ctx, bssEndpoint, map[string]string{
			"Action":       "QueryInstanceBill",
			"Version":      "2017-12-14",
			"BillingCycle": cycle,
			"PageNum":      strconv.Itoa(page),
			"PageSize":     "300",
		})
		if err != nil {
			return nil, err
		}
		var parsed instanceBillResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("解析 BSS 账单响应失败: %w", err)
		}
		for _, it := range parsed.Data.Items.Item {
			name := it.ProductName
			if name == "" {
				name = it.PipCode
			}
			agg[name] += model.ParseMoneyAny(it.PretaxAmount)
		}
		seen += len(parsed.Data.Items.Item)
		if seen >= parsed.Data.TotalCount || len(parsed.Data.Items.Item) == 0 {
			break
		}
	}
	out := make([]model.BillItem, 0, len(agg))
	for product, amount := range agg {
		if amount > 0 {
			out = append(out, model.BillItem{Product: product, Amount: math.Round(amount*100) / 100})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out, nil
}

// InstanceBills 按 cycle（YYYY-MM）返回实例级当月实付账单（同一 QueryInstanceBill
// 链路，聚合前的原始粒度）：实例ID -> 金额合计。供告警数据源把账单过滤到
// 指定计费方式的实例（如按量付费）。分页与 MonthlyBill 同参数同上限。
// 契约校验：响应有账单行却一个 InstanceID 都没有，说明上游结构变了——报错
// 走软降级（billTotal 置 null），绝不把空 map 当"按量实付为 0"喂给告警。
func (c *Client) InstanceBills(ctx context.Context, cycle string) (map[string]float64, error) {
	out := map[string]float64{}
	seen, withID := 0, 0
	for page := 1; page <= 20; page++ {
		body, err := c.call(ctx, bssEndpoint, map[string]string{
			"Action":       "QueryInstanceBill",
			"Version":      "2017-12-14",
			"BillingCycle": cycle,
			"PageNum":      strconv.Itoa(page),
			"PageSize":     "300",
		})
		if err != nil {
			return nil, err
		}
		var parsed instanceBillResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("解析 BSS 账单响应失败: %w", err)
		}
		for _, it := range parsed.Data.Items.Item {
			if it.InstanceID == "" {
				continue // 无实例归属的杂项（账户级费用）不参与按实例过滤
			}
			withID++
			out[it.InstanceID] += model.ParseMoneyAny(it.PretaxAmount)
		}
		seen += len(parsed.Data.Items.Item)
		if seen >= parsed.Data.TotalCount || len(parsed.Data.Items.Item) == 0 {
			break
		}
	}
	if seen > 0 && withID == 0 {
		return nil, fmt.Errorf("BSS 账单响应 %d 行均无 InstanceID，实例级过滤不可用", seen)
	}
	return out, nil
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
		return nil, &APIError{Code: env.Code, Message: env.Message}
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("阿里云 HTTP %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// APIError 是阿里云 RPC 错误信封的类型化形态，供上层按 Code 精确分类
// （如 ListAll 对 Forbidden.RAM 的授权范围外跳过），不靠字符串匹配。
type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("阿里云 %s: %s", e.Code, e.Message)
}

// IsRamDenied 报告错误是否为 RAM 授权拒绝——该资源不在 AK 的授权范围内
//（实例收束策略下，未命中授权实例的地域即返回此码，实测于 2026-10-06）。
func IsRamDenied(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == "Forbidden.RAM"
}
