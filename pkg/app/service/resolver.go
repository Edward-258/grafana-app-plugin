package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	Credential  = model.Credential
)

const LegacyCredentialID = model.LegacyCredentialID

var ErrNoSettings = model.ErrNoSettings

// Enriched 是一行对齐结果。AK/AKLabel 标注命中来源（跨 AK 歧义时缺省）。
type Enriched struct {
	PublicAsset
	MonitorName string `json:"monitorName,omitempty"`
	Note        string `json:"note,omitempty"`
	Matched     bool   `json:"matched"`
	AK          string `json:"ak,omitempty"`
	AKLabel     string `json:"akLabel,omitempty"`
}

// BillingInfo 是下发给前端的单账户概览，带所属 AK 标识。AccountOverview
// 内嵌展开（available/coupon/billItems…），BillByInstance 因 json:"-" 不外发。
type BillingInfo struct {
	AK      string `json:"ak"`
	AKLabel string `json:"akLabel,omitempty"`
	model.AccountOverview
}

// Snapshot 是单个凭证的资产+账户快照，供捆绑 ds 组织告警可查询的数据帧。
type Snapshot struct {
	Cred      Credential
	Instances []model.Instance
	Billing   model.AccountOverview
}

// TestResult 是单个凭证的连通性测试结论，Error 非空即该 AK 失败。
type TestResult struct {
	AK      string `json:"ak"`
	AKLabel string `json:"akLabel,omitempty"`
	Count   int    `json:"count"`
	Regions int    `json:"regions"`
	Error   string `json:"error,omitempty"`
}

const (
	// cacheTTL 缓存新鲜期：窗口内的请求纯内存命中，不触任何拉取。
	cacheTTL = 5 * time.Minute
	// hardStaleCap 硬陈旧上限：超过后不再回旧值、退化为同步刷新，保证
	// 持续失败最终以错误暴露，而不是无限端古董快照。
	hardStaleCap = 30 * time.Minute
	// maxFetchConcurrency 跨 AK 扇出并发上限：每路都是全地域枚举 + BSS 链路，
	// 4 路已能把冷启动压到 ~一倍单 AK 时长，再多只会挤压阿里云侧限流余量。
	maxFetchConcurrency = 4
)

type Resolver struct {
	mu sync.Mutex
	// caches 按 Credential.ID 分片：每个 AK 独立走 SWR/单飞/硬上限，互不拖垮。
	// 插槽内换 AK（同 ID 不同 AccessKeyID）由 ecsCache.akID 比对失效。
	caches map[string]*ecsCache
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
	akID      string
	at        time.Time
	instances []model.Instance
	// 账户概览（余额/代金券/当月账单）是账户级数据，随快照一起缓存（同一凭证键下同生共死）
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
	r := &Resolver{caches: map[string]*ecsCache{}, wantInstanceBills: wantInstanceBills}
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

// Resolve maps one Prometheus identity to the ECS the AKs can see——Enrich 的
// 单条封装，匹配语义（唯一命中、歧义带 note）与列表路径同源。
func (r *Resolver) Resolve(ctx context.Context, creds []Credential, ident Identity) (Enriched, bool, string, error) {
	rows, err := r.Enrich(ctx, creds, []Identity{ident}, false)
	if err != nil || len(rows) == 0 {
		return Enriched{}, false, "", err
	}
	return rows[0], rows[0].Matched, rows[0].Note, nil
}

// Enrich 对齐标识与资产；force=true 时绕过缓存同步实时拉取（手动刷新语义）。
// 跨 AK 匹配语义：单 AK 内唯一命中才 matched；多个 AK 各命中一台 = 歧义
// （与单 AK 内多命中同语义，绝不猜一个）；全部未命中沿用单 AK note。
func (r *Resolver) Enrich(ctx context.Context, creds []Credential, identities []Identity, force bool) ([]Enriched, error) {
	snaps, err := r.snapshots(ctx, creds, force)
	if err != nil {
		return nil, err
	}
	out := make([]Enriched, 0, len(identities))
	for _, ident := range identities {
		row := Enriched{MonitorName: model.MonitorName(ident)}
		var hits []Snapshot
		var hitInst model.Instance
		var notes []string
		for _, s := range snaps {
			inst, _, note, ok := model.MatchIdentity(ident, s.Instances)
			if ok {
				hits = append(hits, s)
				hitInst = inst
			} else if note != "" {
				notes = append(notes, akName(s.Cred)+"："+note)
			}
		}
		switch len(hits) {
		case 1:
			row.PublicAsset = hitInst.Public()
			row.AK = hits[0].Cred.ID
			row.AKLabel = hits[0].Cred.Label
			row.Matched = true
		case 0:
			row.Note = strings.Join(notes, "；")
		default:
			names := make([]string, 0, len(hits))
			for _, s := range hits {
				names = append(names, akName(s.Cred))
			}
			row.Note = fmt.Sprintf("跨 %d 个 AK 歧义: %s", len(hits), strings.Join(names, "/"))
		}
		out = append(out, row)
	}
	return out, nil
}

func akName(c Credential) string {
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

// Test returns how many instances each AK can see, across how many regions.
// 故意绕过缓存直连阿里云：连通性测试要验证的是当下的真实可达性。
// 逐 AK 独立出结果（Error 内联），测试之间互不拖垮。
func (r *Resolver) Test(ctx context.Context, creds []Credential) []TestResult {
	out := make([]TestResult, len(creds))
	forEachLimited(len(creds), maxFetchConcurrency, func(i int) {
		res := TestResult{AK: creds[i].ID, AKLabel: creds[i].Label}
		list, err := r.fetch(ctx, creds[i].Config())
		if err != nil {
			res.Error = err.Error()
			out[i] = res
			return
		}
		r.applyBss(ctx, creds[i].Config(), list) // 与正常快照同一合入规则，保证缓存语义一致
		r.store(creds[i], list, r.applyBilling(ctx, creds[i].Config()))
		res.Count = len(list)
		res.Regions = len(model.RegionSet(list))
		out[i] = res
	})
	return out
}

// Billings 返回各凭证缓存里的账户概览（无缓存/BSS 整体软失败时该 AK 缺省），
// 供 attachBillings 组装响应。只读缓存，不触网络。
func (r *Resolver) Billings(creds []Credential) []BillingInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]BillingInfo, 0, len(creds))
	for _, c := range creds {
		if cache := r.caches[c.ID]; cache != nil && cache.akID == c.AccessKeyID && !cache.billing.IsZero() {
			out = append(out, BillingInfo{AK: c.ID, AKLabel: c.Label, AccountOverview: cache.billing})
		}
	}
	return out
}

// Ensure 检查全部凭证可用：任何 AK 失败都进错误信息（点名 label），健康检查
// 要的是"全部可用"，与 enrich 的独立降级语义不同。
func (r *Resolver) Ensure(ctx context.Context, creds []Credential) error {
	errs := make([]string, len(creds)) // 按下标写，守 forEachLimited 的槽位隔离约定
	forEachLimited(len(creds), maxFetchConcurrency, func(i int) {
		if _, err := r.instances(ctx, creds[i], false); err != nil {
			errs[i] = fmt.Sprintf("AK「%s」: %s", akName(creds[i]), err.Error())
		}
	})
	if errs = slices.DeleteFunc(errs, func(s string) bool { return s == "" }); len(errs) > 0 {
		return errors.New(strings.Join(errs, "；"))
	}
	return nil
}

// Snapshot 返回全部凭证的资产与账户概览，供捆绑数据源（pkg/ds）组织告警可
// 查询的数据帧。帧字段由调用方白名单挑选——Instance 携带的 IP 字段不得出现
// 在任何帧里。单个 AK 失败时该 Snapshot 缺席（帧里对应序列落 NoData）。
func (r *Resolver) Snapshot(ctx context.Context, creds []Credential, force bool) ([]Snapshot, error) {
	return r.snapshots(ctx, creds, force)
}

// snapshots 扇出取全部凭证的快照（有限并发）。失败 AK 只记日志、不进结果，
// 不拖垮其它 AK（跨 AK 独立降级，2026-10-03 拍板：红线3 的"禁止部分结果"
// 指单 AK 内全地域完整性，由 fetch 内部保证；跨 AK 是独立账号，逐个降级
// 不违背本意）；全部失败时返回首个错误，保持失败可见。
func (r *Resolver) snapshots(ctx context.Context, creds []Credential, force bool) ([]Snapshot, error) {
	out := make([]Snapshot, len(creds))
	var firstErr error
	var mu sync.Mutex
	forEachLimited(len(creds), maxFetchConcurrency, func(i int) {
		list, err := r.instances(ctx, creds[i], force)
		if err != nil {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
			log.DefaultLogger.Error("AK 快照失败，本轮跳过该 AK", "slot", creds[i].ID, "label", creds[i].Label, "error", err.Error())
			return
		}
		out[i] = Snapshot{Cred: creds[i], Instances: list, Billing: r.cachedBilling(creds[i])}
	})
	n := 0
	for _, s := range out {
		if s.Cred.ID != "" {
			out[n] = s
			n++
		}
	}
	if n == 0 {
		return nil, firstErr
	}
	return out[:n], nil
}

func (r *Resolver) cachedBilling(cred Credential) model.AccountOverview {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.caches[cred.ID]; c != nil && c.akID == cred.AccessKeyID {
		return c.billing
	}
	return model.AccountOverview{}
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
// 缓存命中额外要求 akID 一致：同插槽换 AK 后旧快照立即作废。
func (r *Resolver) instances(ctx context.Context, cred Credential, force bool) ([]model.Instance, error) {
	if !force {
		r.mu.Lock()
		if c := r.caches[cred.ID]; c != nil && c.akID == cred.AccessKeyID {
			age := time.Since(c.at)
			if age < cacheTTL {
				r.mu.Unlock()
				return c.instances, nil
			}
			if age < hardStaleCap {
				if !c.refreshing {
					c.refreshing = true
					go r.backgroundRefresh(cred)
				}
				r.mu.Unlock()
				return c.instances, nil
			}
			// 超过硬上限：数据太旧，落到同步刷新
		}
		r.mu.Unlock()
	}
	return r.refresh(ctx, cred)
}

func (r *Resolver) refresh(ctx context.Context, cred Credential) ([]model.Instance, error) {
	list, err := r.fetch(ctx, cred.Config())
	if err != nil {
		return nil, err
	}
	r.applyBss(ctx, cred.Config(), list)
	r.store(cred, list, r.applyBilling(ctx, cred.Config()))
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
func (r *Resolver) backgroundRefresh(cred Credential) {
	list, err := r.fetch(context.Background(), cred.Config())
	if err == nil {
		r.applyBss(context.Background(), cred.Config(), list)
	}
	var billing model.AccountOverview
	if err == nil {
		billing = r.applyBilling(context.Background(), cred.Config())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.caches[cred.ID]
	if c == nil || c.akID != cred.AccessKeyID {
		return // 缓存槽已因插槽删除或换 AK 重置，本次结果作废
	}
	if err != nil {
		c.refreshing = false // 放行下一次触发
		log.DefaultLogger.Error("后台刷新资产列表失败，继续使用旧快照", "error", err)
		return
	}
	r.caches[cred.ID] = &ecsCache{
		akID: cred.AccessKeyID, at: time.Now(), instances: list,
		billing: billing,
	}
}

func (r *Resolver) store(cred Credential, list []model.Instance, billing model.AccountOverview) {
	r.mu.Lock()
	r.caches[cred.ID] = &ecsCache{
		akID: cred.AccessKeyID, at: time.Now(), instances: list,
		billing: billing,
	}
	r.mu.Unlock()
}

// forEachLimited 以有限并发跑 n 项任务。不引入 x/sync：任务轻、上限小，
// 一个工作池足够；结果写入由调用方自行保证槽位隔离（fn(i) 只写 i）。
func forEachLimited(n, limit int, fn func(i int)) {
	if n <= 0 {
		return
	}
	if limit > n {
		limit = n
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	for range limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				fn(i)
			}
		}()
	}
	for i := range n {
		idx <- i
	}
	close(idx)
	wg.Wait()
}
