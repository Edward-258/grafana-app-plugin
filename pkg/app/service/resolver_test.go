package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local-ecs-app/pkg/aliyun/model"
)

func newTestResolver(fetch func(ctx context.Context, cfg Config) ([]model.Instance, error)) *Resolver {
	r := NewResolver()
	r.fetch = fetch
	// 默认屏蔽 BSS 真实网络调用；需要 BSS 语义的用例自行覆写
	r.fetchBss = func(context.Context, Config) (map[string]string, error) { return nil, nil }
	return r
}

func waitCache(t *testing.T, r *Resolver, wantID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := ""
		if len(r.cache.instances) > 0 {
			got = r.cache.instances[0].InstanceID
		}
		r.mu.Unlock()
		if got == wantID {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待缓存变为 %s 超时", wantID)
}

func waitCalls(t *testing.T, calls *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(calls) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 fetch 调用数达到 %d 超时 (got %d)", want, atomic.LoadInt32(calls))
}

// 冷启动：无缓存 → 同步阻塞拉取并落缓存；新鲜期内不再拉。
func TestColdFetchBlocksThenFreshHits(t *testing.T) {
	var calls int32
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}

	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || len(got) != 1 || got[0].InstanceID != "i-new" {
		t.Fatalf("冷启动应同步返回新值, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("应恰好拉取 1 次, got %d", calls)
	}
	if _, err := r.instances(context.Background(), cfg, false); err != nil || atomic.LoadInt32(&calls) != 1 {
		t.Errorf("新鲜期内应纯内存命中不重复拉取, calls=%d err=%v", calls, err)
	}
}

// 过期但在硬上限内：立即返回旧值，后台单飞刷新后替换为新值。
func TestStaleServesOldThenBackgroundReplaces(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		<-release // 挂住后台刷新，证明旧值先行
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}
	r.store(cfg, []model.Instance{{InstanceID: "i-old"}})
	r.cache.at = time.Now().Add(-2 * cacheTTL)

	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || got[0].InstanceID != "i-old" {
		t.Fatalf("过期后应立即返回旧值, got %v err=%v", got, err)
	}
	waitCalls(t, &calls, 1) // 后台 goroutine 已被调度并进入 fetch
	close(release)
	waitCache(t, r, "i-new")
}

// 并发多个过期请求：全部秒回旧值，且只触发一次后台刷新（单飞）。
func TestStaleSingleFlight(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}
	r.store(cfg, []model.Instance{{InstanceID: "i-old"}})
	r.cache.at = time.Now().Add(-2 * cacheTTL)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.instances(context.Background(), cfg, false)
			if err != nil || got[0].InstanceID != "i-old" {
				t.Errorf("并发过期请求应回旧值, got %v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
	waitCalls(t, &calls, 1)
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("10 个并发请求应只触发 1 次后台刷新, got %d", calls)
	}
	close(release)
}

// 超过硬陈旧上限：不再回旧值，退化为同步刷新返回新数据。
func TestHardStaleCapBlocks(t *testing.T) {
	var calls int32
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}
	r.store(cfg, []model.Instance{{InstanceID: "i-old"}})
	r.cache.at = time.Now().Add(-(hardStaleCap + time.Minute))

	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || got[0].InstanceID != "i-new" {
		t.Fatalf("超过硬上限应同步刷新返回新值, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("应恰好同步拉取 1 次, got %d", calls)
	}
}

// 后台刷新失败：保留旧快照继续服务，并放行下一次触发重试。
func TestBackgroundFailureKeepsStaleAndRetries(t *testing.T) {
	var calls int32
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, errors.New("阿里云抽风")
		}
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}
	r.store(cfg, []model.Instance{{InstanceID: "i-old"}})
	r.cache.at = time.Now().Add(-2 * cacheTTL)

	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || got[0].InstanceID != "i-old" {
		t.Fatalf("后台失败不应影响本次响应, got %v err=%v", got, err)
	}
	// 等第一次后台刷新失败完成（refreshing 被清掉）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		busy := r.cache.refreshing
		r.mu.Unlock()
		if !busy && atomic.LoadInt32(&calls) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 再次请求：旧值照回，且会再次触发刷新（第二次成功）
	if _, err := r.instances(context.Background(), cfg, false); err != nil {
		t.Fatalf("重试请求不应报错: %v", err)
	}
	waitCache(t, r, "i-new")
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("失败后应重试一次, calls=%d", calls)
	}
}

// 手动刷新：新鲜期内 force 也必须真拉一次，且结果回写缓存供后续普通读命中。
func TestForceRefreshBypassesFreshCache(t *testing.T) {
	var calls int32
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		return []model.Instance{{InstanceID: "i-new"}}, nil
	})
	cfg := Config{AccessKeyID: "ak"}
	r.store(cfg, []model.Instance{{InstanceID: "i-old"}}) // 此刻缓存新鲜

	// 新鲜期普通读：纯内存命中
	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || got[0].InstanceID != "i-old" || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("新鲜期普通读应命中缓存, got %v calls=%d err=%v", got, calls, err)
	}

	// force：无视新鲜度，同步实拉并拿到新值
	got, err = r.instances(context.Background(), cfg, true)
	if err != nil || got[0].InstanceID != "i-new" {
		t.Fatalf("force 应同步实时拉取, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("force 应恰好实拉 1 次, got %d", calls)
	}

	// force 的结果已回写：后续普通读直接命中新值，不再拉
	got, err = r.instances(context.Background(), cfg, false)
	if err != nil || got[0].InstanceID != "i-new" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("force 后普通读应命中新缓存, got %v calls=%d err=%v", got, calls, err)
	}
}

// BSS 补充链路：刷新时把订单口径开通时间写入 LeaseStartTime（独立字段），
// ECS CreationTime 保持原值；命中缓存与强制刷新都吃到同一路数据。
func TestBssSuppliesLeaseStart(t *testing.T) {
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		return []model.Instance{{InstanceID: "i-x", CreationTime: "2026-01-01T00:00Z"}}, nil
	})
	var bssCalls int32
	r.fetchBss = func(_ context.Context, _ Config) (map[string]string, error) {
		atomic.AddInt32(&bssCalls, 1)
		return map[string]string{"i-x": "2026-06-23T06:09:41Z"}, nil
	}
	cfg := Config{AccessKeyID: "ak"}

	got, err := r.instances(context.Background(), cfg, false)
	if err != nil || got[0].LeaseStartTime != "2026-06-23T06:09:41Z" {
		t.Fatalf("BSS 时间应写入 LeaseStartTime, got %v err=%v", got, err)
	}
	if got[0].CreationTime != "2026-01-01T00:00Z" {
		t.Fatalf("ECS CreationTime 不应被 BSS 覆盖, got %q", got[0].CreationTime)
	}

	// 新鲜期普通读：纯内存命中，BSS 不再被调
	if _, err := r.instances(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&bssCalls) != 1 {
		t.Fatalf("新鲜期不应重复调 BSS, calls=%d", bssCalls)
	}

	// 强制刷新：BSS 与 ECS 同批重拉
	if _, err := r.instances(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&bssCalls) != 2 {
		t.Fatalf("force 应连带 BSS 重拉, calls=%d", bssCalls)
	}
}

// BSS 软降级：补充链路失败不影响主链路，LeaseStartTime 留空、CreationTime 保留 ECS 值。
func TestBssFailureKeepsEcsTime(t *testing.T) {
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		return []model.Instance{{InstanceID: "i-x", CreationTime: "2026-01-01T00:00Z"}}, nil
	})
	r.fetchBss = func(context.Context, Config) (map[string]string, error) {
		return nil, errors.New("bss 无权限")
	}

	got, err := r.instances(context.Background(), Config{AccessKeyID: "ak"}, false)
	if err != nil || got[0].CreationTime != "2026-01-01T00:00Z" || got[0].LeaseStartTime != "" {
		t.Fatalf("BSS 失败应留空 leaseStart 且保留 ECS 时间, got %v err=%v", got, err)
	}
}
