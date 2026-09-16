package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"local-ecs-app/pkg/aliyun/client"
)

type Identity = client.Identity
type Config = client.Config
type PublicAsset = client.PublicAsset

var ErrNoSettings = client.ErrNoSettings

type Enriched struct {
	PublicAsset
	MonitorName string `json:"monitorName,omitempty"`
	Matched     bool   `json:"matched"`
}

type Resolver struct {
	mu    sync.Mutex
	cache *ecsCache
}

type ecsCache struct {
	key       string
	at        time.Time
	instances []client.Instance
}

func NewResolver() *Resolver {
	return &Resolver{}
}

func (r *Resolver) Resolve(ctx context.Context, cfg Config, ident Identity) (PublicAsset, bool, error) {
	ip := strings.TrimSpace(ident.IP)
	if ip == "" {
		ip = client.IPFromIdentity(ident.Instance)
	}
	if ip == "" {
		ip = client.IPFromIdentity(ident.NodeName)
	}
	if ip == "" {
		list, err := r.instances(ctx, cfg)
		if err != nil {
			return PublicAsset{}, false, err
		}
		inst, _, ok := client.MatchIdentity(ident, list)
		if !ok {
			return PublicAsset{}, false, nil
		}
		if len(inst.PrivateIPs) > 0 {
			ip = inst.PrivateIPs[0]
		} else if len(inst.PublicIPs) > 0 {
			ip = inst.PublicIPs[0]
		} else {
			return inst.Public(), true, nil
		}
	}

	found, ok, err := client.New(cfg).GetByIP(ctx, ip)
	if err != nil {
		return PublicAsset{}, false, err
	}
	if ok {
		return found.Public(), true, nil
	}
	return PublicAsset{}, false, nil
}

func (r *Resolver) Enrich(ctx context.Context, cfg Config, identities []Identity) ([]Enriched, error) {
	out := make([]Enriched, 0, len(identities))
	for _, ident := range identities {
		row := Enriched{MonitorName: client.MonitorName(ident)}
		asset, ok, err := r.Resolve(ctx, cfg, ident)
		if err != nil {
			return nil, err
		}
		if ok {
			row.PublicAsset = asset
			row.Matched = true
		}
		out = append(out, row)
	}
	return out, nil
}

func (r *Resolver) Test(ctx context.Context, cfg Config) (int, error) {
	list, err := client.New(cfg).List(ctx)
	if err != nil {
		return 0, err
	}
	r.store(cfg, list)
	return len(list), nil
}

func (r *Resolver) MonitorName(ident Identity) string {
	return client.MonitorName(ident)
}

func (r *Resolver) Ensure(ctx context.Context, cfg Config) error {
	_, err := r.instances(ctx, cfg)
	return err
}

func (r *Resolver) instances(ctx context.Context, cfg Config) ([]client.Instance, error) {
	key := cfg.Region + "|" + cfg.AccessKeyID
	r.mu.Lock()
	if r.cache != nil && r.cache.key == key && time.Since(r.cache.at) < 5*time.Minute {
		out := r.cache.instances
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()

	list, err := client.New(cfg).List(ctx)
	if err != nil {
		return nil, err
	}
	r.store(cfg, list)
	return list, nil
}

func (r *Resolver) store(cfg Config, list []client.Instance) {
	r.mu.Lock()
	r.cache = &ecsCache{key: cfg.Region + "|" + cfg.AccessKeyID, at: time.Now(), instances: list}
	r.mu.Unlock()
}
