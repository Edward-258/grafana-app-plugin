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

	got, err := r.instances(context.Background(), cfg)
	if err != nil || len(got) != 1 || got[0].InstanceID != "i-new" {
		t.Fatalf("冷启动应同步返回新值, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("应恰好拉取 1 次, got %d", calls)
	}
	if _, err := r.instances(context.Background(), cfg); err != nil || atomic.LoadInt32(&calls) != 1 {
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

	got, err := r.instances(context.Background(), cfg)
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
			got, err := r.instances(context.Background(), cfg)
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

	got, err := r.instances(context.Background(), cfg)
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

	got, err := r.instances(context.Background(), cfg)
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
	if _, err := r.instances(context.Background(), cfg); err != nil {
		t.Fatalf("重试请求不应报错: %v", err)
	}
	waitCache(t, r, "i-new")
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("失败后应重试一次, calls=%d", calls)
	}
}
