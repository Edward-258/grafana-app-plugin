package service

import (
	"context"
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
	// fetch 抽象"全量拉取"动作以便测试注入；生产实现是阿里云全地域扫描。
	fetch func(ctx context.Context, cfg Config) ([]model.Instance, error)
	// fetchBss 是 BSS「已购资源」补充链路（精确创建时间，订单口径）。
	// 失败软降级：日志告警后保留 ECS CreationTime，不打断资产主链路。
	fetchBss func(ctx context.Context, cfg Config) (map[string]string, error)
}

type ecsCache struct {
	key        string
	at         time.Time
	instances  []model.Instance
	refreshing bool // 单飞标记：一轮后台刷新在飞时不重复触发
}

func NewResolver() *Resolver {
	return &Resolver{
		fetch: func(ctx context.Context, cfg Config) ([]model.Instance, error) {
			return client.New(cfg).ListAll(ctx)
		},
		fetchBss: func(ctx context.Context, cfg Config) (map[string]string, error) {
			return client.New(cfg).CreationTimes(ctx)
		},
	}
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
	r.store(cfg, list)
	return len(list), len(model.RegionSet(list)), nil
}

func (r *Resolver) Ensure(ctx context.Context, cfg Config) error {
	_, err := r.instances(ctx, cfg, false)
	return err
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
	r.store(cfg, list)
	return list, nil
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
	r.cache = &ecsCache{key: cfg.AccessKeyID, at: time.Now(), instances: list}
}

func (r *Resolver) store(cfg Config, list []model.Instance) {
	r.mu.Lock()
	r.cache = &ecsCache{key: cfg.AccessKeyID, at: time.Now(), instances: list}
	r.mu.Unlock()
}
