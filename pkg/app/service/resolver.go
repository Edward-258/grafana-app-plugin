package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend/log"

	"local-ecs-app/pkg/aliyun/client"
	"local-ecs-app/pkg/aliyun/model"
)

// 门面类型：handler 只 import service，领域定义本体在 model。
type (
	Identity    = model.Identity
	Config      = model.Config
	PublicAsset = model.PublicAsset
)

var ErrNoSettings = model.ErrNoSettings

type Enriched struct {
	PublicAsset
	MonitorName string `json:"monitorName,omitempty"`
	Note        string `json:"note,omitempty"`
	Matched     bool   `json:"matched"`
}

const (
	// cacheTTL 缓存新鲜期：窗口内的请求纯内存命中，不触任何拉取。
	cacheTTL = 5 * time.Minute
	// hardStaleCap 硬陈旧上限：超过后不再回旧值、退化为同步刷新，保证
	// 持续失败最终以错误暴露，而不是无限端古董快照。
	hardStaleCap = 30 * time.Minute
)

type Resolver struct {
	mu    sync.Mutex
	cache *ecsCache
	// wantInstanceBills：告警数据源需要实例级账单（按计费方式过滤），app 侧
	// 解析器不需要（多一次 BSS 分页调用，不为此付费）。见 NewResolverForAlerting。
	wantInstanceBills bool
	// fetch 抽象"全量拉取"动作以便测试注入；生产实现是阿里云全地域扫描。
	fetch func(ctx context.Context, cfg Config) ([]model.Instance, error)
	// fetchBss 是 BSS「已购资源」补充链路（订单口径租赁开始时间）。
	// 失败软降级：日志告警后字段留空，不打断资产主链路。
	fetchBss func(ctx context.Context, cfg Config) (map[string]string, error)
	// fetchBilling 是 BSS 账户概览链路（余额/代金券/当月账单聚合）。
	// 同样软降级：部分失败保留已得数据，整体失败置零省略下发。
	fetchBilling func(ctx context.Context, cfg Config) (model.AccountOverview, error)
}

type ecsCache struct {
	key       string
	at        time.Time
	instances []model.Instance
	// 账户概览（余额/代金券/当月账单）是账户级数据，随快照一起缓存（同一 AK 键下同生共死）
	billing    model.AccountOverview
	refreshing bool // 单飞标记：一轮后台刷新在飞时不重复触发
}

func NewResolver() *Resolver {
	return newResolver(false)
}

// NewResolverForAlerting 供告警数据源使用：在资产/账户之外额外拉取实例级
// 账单（多一次 BSS 分页调用），使 account 帧的 billTotal 能过滤到指定计费
// 方式的实例（如只统计按量付费）。
func NewResolverForAlerting() *Resolver {
	return newResolver(true)
}

func newResolver(wantInstanceBills bool) *Resolver {
	r := &Resolver{wantInstanceBills: wantInstanceBills}
	r.fetch = func(ctx context.Context, cfg Config) ([]model.Instance, error) {
		return client.New(cfg).ListAll(ctx)
	}
	r.fetchBss = func(ctx context.Context, cfg Config) (map[string]string, error) {
		return client.New(cfg).CreationTimes(ctx)
	}
	r.fetchBilling = r.fetchBillingImpl
	return r
}

// fetchBillingImpl 账户概览链路：余额 + 按产品聚合的当月账单（app UI 口径）；
// 告警解析器再补充实例级账单（软失败：缺它只影响 ds 的 billTotal，评估落
// NoData，不打断余额/账单主数据）。
func (r *Resolver) fetchBillingImpl(ctx context.Context, cfg Config) (model.AccountOverview, error) {
	c := client.New(cfg)
	ov, err := c.AccountBalance(ctx)
	if err != nil {
		return model.AccountOverview{}, err
	}
	cycle := time.Now().Format("2006-01")
	items, err := c.MonthlyBill(ctx, cycle)
	if err != nil {
		return ov, fmt.Errorf("余额已取、账单失败: %w", err)
	}
	ov.BillingCycle = cycle
	for _, it := range items {
		ov.BillTotal += it.Amount
	}
	ov.BillItems = items
	if r.wantInstanceBills {
		byInstance, err := c.InstanceBills(ctx, cycle)
		if err != nil {
			return ov, fmt.Errorf("余额/账单已取、实例级账单失败: %w", err)
		}
		ov.BillByInstance = byInstance
	}
	return ov, nil
}

// Resolve maps one Prometheus identity to the ECS the AK can see——Enrich 的
// 单条封装，匹配语义（严格唯一命中、歧义带 note）与列表路径同源。
func (r *Resolver) Resolve(ctx context.Context, cfg Config, ident Identity) (PublicAsset, bool, string, error) {
	rows, err := r.Enrich(ctx, cfg, []Identity{ident}, false)
	if err != nil || len(rows) == 0 {
		return PublicAsset{}, false, "", err
	}
	return rows[0].PublicAsset, rows[0].Matched, rows[0].Note, nil
}

// Enrich 对齐标识与资产；force=true 时绕过缓存同步实时拉取（手动刷新语义）。
func (r *Resolver) Enrich(ctx context.Context, cfg Config, identities []Identity, force bool) ([]Enriched, error) {
	list, err := r.instances(ctx, cfg, force)
	if err != nil {
		return nil, err
	}
	out := make([]Enriched, 0, len(identities))
	for _, ident := range identities {
		row := Enriched{MonitorName: model.MonitorName(ident)}
		inst, _, note, ok := model.MatchIdentity(ident, list)
		if ok {
			row.PublicAsset = inst.Public()
			row.Matched = true
		} else {
			row.Note = note
		}
		out = append(out, row)
	}
	return out, nil
}

// Test returns how many instances the AK can see, across how many regions.
// 故意绕过缓存直连阿里云：连通性测试要验证的是当下的真实可达性。
func (r *Resolver) Test(ctx context.Context, cfg Config) (int, int, error) {
	list, err := r.fetch(ctx, cfg)
	if err != nil {
		return 0, 0, err
	}
	r.applyBss(ctx, cfg, list) // 与正常快照同一合入规则，保证缓存语义一致
	r.store(cfg, list, r.applyBilling(ctx, cfg))
	return len(list), len(model.RegionSet(list)), nil
}

// Billing 返回最近一次快照携带的账户概览（无缓存/整体软失败时 ok=false）。
func (r *Resolver) Billing() (model.AccountOverview, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.cache; c != nil && !c.billing.IsZero() {
		return c.billing, true
	}
	return model.AccountOverview{}, false
}

func (r *Resolver) Ensure(ctx context.Context, cfg Config) error {
	_, err := r.instances(ctx, cfg, false)
	return err
}

// Snapshot 返回全量资产与账户概览，供捆绑数据源（pkg/ds）组织告警可查询的
// 数据帧。缓存语义与 Enrich 完全一致（SWR/单飞/全地域完整性约束）。
// 帧字段由调用方白名单挑选——Instance 携带的 IP 字段不得出现在任何帧里。
func (r *Resolver) Snapshot(ctx context.Context, cfg Config, force bool) ([]model.Instance, model.AccountOverview, error) {
	list, err := r.instances(ctx, cfg, force)
	if err != nil {
		return nil, model.AccountOverview{}, err
	}
	ov, _ := r.Billing()
	return list, ov, nil
}

// instances 是 stale-while-revalidate 的核心，三岔：
//   - 新鲜（< TTL）→ 纯内存命中；
//   - 过期但在硬上限内 → 立即返回旧快照，必要时单飞触发后台刷新；
//   - 超过硬上限或无缓存 → 同步全量刷新，请求方等待、错误如实返回。
//
// force=true 是第四条路：无视新鲜度直接同步全量拉取并回写缓存——手动刷新
// 要的就是"实打实的一次实时请求"，缓存只用来兜普通读流量。
//
// 完整性语义不变：每次刷新仍是全地域扫描、任一地域失败作废，绝不端
// 半份结果；后台刷新失败只是"继续用上一份完整快照"，下次请求再试。
func (r *Resolver) instances(ctx context.Context, cfg Config, force bool) ([]model.Instance, error) {
	if !force {
		r.mu.Lock()
		if c := r.cache; c != nil && c.key == cfg.AccessKeyID {
			age := time.Since(c.at)
			if age < cacheTTL {
				r.mu.Unlock()
				return c.instances, nil
			}
			if age < hardStaleCap {
				if !c.refreshing {
					c.refreshing = true
					go r.backgroundRefresh(cfg)
				}
				r.mu.Unlock()
				return c.instances, nil
			}
			// 超过硬上限：数据太旧，落到同步刷新
		}
		r.mu.Unlock()
	}
	return r.refresh(ctx, cfg)
}

func (r *Resolver) refresh(ctx context.Context, cfg Config) ([]model.Instance, error) {
	list, err := r.fetch(ctx, cfg)
	if err != nil {
		return nil, err
	}
	r.applyBss(ctx, cfg, list)
	r.store(cfg, list, r.applyBilling(ctx, cfg))
	return list, nil
}

// applyBilling 取账户概览（余额/代金券/当月账单），软失败返回零值；
// 部分失败时 fetchBilling 已尽量保留已得数据（如余额成功账单失败）。
func (r *Resolver) applyBilling(ctx context.Context, cfg Config) model.AccountOverview {
	ov, err := r.fetchBilling(ctx, cfg)
	if err != nil {
		log.DefaultLogger.Error("BSS 账户概览部分失败，保留已得数据", "error", err)
	}
	return ov
}

// applyBss 把 BSS 订单口径的开通时间写入快照的 LeaseStartTime（独立字段，
// 不动 ECS CreationTime），随 SWR 缓存、单飞与强制刷新自然复用——三条刷新
// 路径（同步/后台/force）都经过这里。软失败：BSS 不可用时字段留空，主链路照常。
func (r *Resolver) applyBss(ctx context.Context, cfg Config, list []model.Instance) {
	bss, err := r.fetchBss(ctx, cfg)
	if err != nil {
		log.DefaultLogger.Error("BSS 租赁开始时间补充失败，LeaseStart 置空", "error", err)
		return
	}
	for i := range list {
		if t := bss[list[i].InstanceID]; t != "" {
			list[i].LeaseStartTime = t
		}
	}
}

// backgroundRefresh 必须用 context.Background：触发它的请求一旦返回，
// 其 ctx 即被取消——用请求 ctx 做后台刷新会被半路掐死（SWR 经典坑）。
func (r *Resolver) backgroundRefresh(cfg Config) {
	list, err := r.fetch(context.Background(), cfg)
	if err == nil {
		r.applyBss(context.Background(), cfg, list)
	}
	var billing model.AccountOverview
	if err == nil {
		billing = r.applyBilling(context.Background(), cfg)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.cache
	if c == nil || c.key != cfg.AccessKeyID {
		return // 缓存槽已因换 AK 重置，本次结果作废
	}
	if err != nil {
		c.refreshing = false // 放行下一次触发
		log.DefaultLogger.Error("后台刷新资产列表失败，继续使用旧快照", "error", err)
		return
	}
	r.cache = &ecsCache{
		key: cfg.AccessKeyID, at: time.Now(), instances: list,
		billing: billing,
	}
}

func (r *Resolver) store(cfg Config, list []model.Instance, billing model.AccountOverview) {
	r.mu.Lock()
	r.cache = &ecsCache{
		key: cfg.AccessKeyID, at: time.Now(), instances: list,
		billing: billing,
	}
	r.mu.Unlock()
}
