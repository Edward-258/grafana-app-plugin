package service

import (
	"context"
	"sync"
	"time"

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

type Resolver struct {
	mu    sync.Mutex
	cache *ecsCache
}

type ecsCache struct {
	key       string
	at        time.Time
	instances []model.Instance
}

func NewResolver() *Resolver {
	return &Resolver{}
}

// Resolve maps one Prometheus identity to the ECS the AK can see. Matching is
// strict: a weak identifier hitting several instances stays unmatched and the
// note says why.
func (r *Resolver) Resolve(ctx context.Context, cfg Config, ident Identity) (PublicAsset, bool, string, error) {
	list, err := r.instances(ctx, cfg)
	if err != nil {
		return PublicAsset{}, false, "", err
	}
	inst, _, note, ok := model.MatchIdentity(ident, list)
	if !ok {
		return PublicAsset{}, false, note, nil
	}
	return inst.Public(), true, "", nil
}

func (r *Resolver) Enrich(ctx context.Context, cfg Config, identities []Identity) ([]Enriched, error) {
	list, err := r.instances(ctx, cfg)
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
	list, err := client.New(cfg).ListAll(ctx)
	if err != nil {
		return 0, 0, err
	}
	r.store(cfg, list)
	return len(list), len(model.RegionSet(list)), nil
}

func (r *Resolver) Ensure(ctx context.Context, cfg Config) error {
	_, err := r.instances(ctx, cfg)
	return err
}

// instances caches the full multi-region picture per AccessKey for a few
// minutes; the first call after expiry pays one DescribeRegions plus one
// DescribeInstances sweep per region.
func (r *Resolver) instances(ctx context.Context, cfg Config) ([]model.Instance, error) {
	r.mu.Lock()
	if r.cache != nil && r.cache.key == cfg.AccessKeyID && time.Since(r.cache.at) < 5*time.Minute {
		out := r.cache.instances
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()

	list, err := client.New(cfg).ListAll(ctx)
	if err != nil {
		return nil, err
	}
	r.store(cfg, list)
	return list, nil
}

func (r *Resolver) store(cfg Config, list []model.Instance) {
	r.mu.Lock()
	r.cache = &ecsCache{key: cfg.AccessKeyID, at: time.Now(), instances: list}
	r.mu.Unlock()
}
