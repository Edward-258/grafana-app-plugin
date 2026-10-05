package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"local-ecs-app/pkg/aliyun/model"
)

func testCred(id, akID string) Credential {
	return Credential{ID: id, Label: "标签-" + id, AccessKeyID: akID, AccessKeySecret: "sk-" + id}
}

func newTestResolver(fetch func(ctx context.Context, cfg Config) ([]model.Instance, error)) *Resolver {
	r := NewResolver()
	r.fetch = fetch
	// 默认屏蔽 BSS 真实网络调用；需要 BSS 语义的用例自行覆写
	r.fetchBss = func(context.Context, Config) (map[string]string, error) { return nil, nil }
	r.fetchBilling = func(context.Context, Config) (model.AccountOverview, error) {
		return model.AccountOverview{}, nil
	}
	return r
}

func waitCache(t *testing.T, r *Resolver, cred Credential, wantID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := ""
		if c := r.caches[cred.ID]; c != nil && len(c.instances) > 0 {
			got = c.instances[0].InstanceID
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
	cred := testCred("s1", "ak")

	got, err := r.instances(context.Background(), cred, false)
	if err != nil || len(got) != 1 || got[0].InstanceID != "i-new" {
		t.Fatalf("冷启动应同步返回新值, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("应恰好拉取 1 次, got %d", calls)
	}
	if _, err := r.instances(context.Background(), cred, false); err != nil || atomic.LoadInt32(&calls) != 1 {
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
	cred := testCred("s1", "ak")
	r.store(cred, []model.Instance{{InstanceID: "i-old"}}, model.AccountOverview{})
	r.caches[cred.ID].at = time.Now().Add(-2 * cacheTTL)

	got, err := r.instances(context.Background(), cred, false)
	if err != nil || got[0].InstanceID != "i-old" {
		t.Fatalf("过期后应立即返回旧值, got %v err=%v", got, err)
	}
	waitCalls(t, &calls, 1) // 后台 goroutine 已被调度并进入 fetch
	close(release)
	waitCache(t, r, cred, "i-new")
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
	cred := testCred("s1", "ak")
	r.store(cred, []model.Instance{{InstanceID: "i-old"}}, model.AccountOverview{})
	r.caches[cred.ID].at = time.Now().Add(-2 * cacheTTL)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := r.instances(context.Background(), cred, false)
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
	cred := testCred("s1", "ak")
	r.store(cred, []model.Instance{{InstanceID: "i-old"}}, model.AccountOverview{})
	r.caches[cred.ID].at = time.Now().Add(-(hardStaleCap + time.Minute))

	got, err := r.instances(context.Background(), cred, false)
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
	cred := testCred("s1", "ak")
	r.store(cred, []model.Instance{{InstanceID: "i-old"}}, model.AccountOverview{})
	r.caches[cred.ID].at = time.Now().Add(-2 * cacheTTL)

	got, err := r.instances(context.Background(), cred, false)
	if err != nil || got[0].InstanceID != "i-old" {
		t.Fatalf("后台失败不应影响本次响应, got %v err=%v", got, err)
	}
	// 等第一次后台刷新失败完成（refreshing 被清掉）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		busy := r.caches[cred.ID].refreshing
		r.mu.Unlock()
		if !busy && atomic.LoadInt32(&calls) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 再次请求：旧值照回，且会再次触发刷新（第二次成功）
	if _, err := r.instances(context.Background(), cred, false); err != nil {
		t.Fatalf("重试请求不应报错: %v", err)
	}
	waitCache(t, r, cred, "i-new")
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
	cred := testCred("s1", "ak")
	r.store(cred, []model.Instance{{InstanceID: "i-old"}}, model.AccountOverview{}) // 此刻缓存新鲜

	// 新鲜期普通读：纯内存命中
	got, err := r.instances(context.Background(), cred, false)
	if err != nil || got[0].InstanceID != "i-old" || atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("新鲜期普通读应命中缓存, got %v calls=%d err=%v", got, calls, err)
	}

	// force：无视新鲜度，同步实拉并拿到新值
	got, err = r.instances(context.Background(), cred, true)
	if err != nil || got[0].InstanceID != "i-new" {
		t.Fatalf("force 应同步实时拉取, got %v err=%v", got, err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("force 应恰好实拉 1 次, got %d", calls)
	}

	// force 的结果已回写：后续普通读直接命中新值，不再拉
	got, err = r.instances(context.Background(), cred, false)
	if err != nil || got[0].InstanceID != "i-new" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("force 后普通读应命中新缓存, got %v calls=%d err=%v", got, calls, err)
	}
}

// 同插槽换 AK：旧快照立即作废（akID 比对失效），绝不让新 AK 读到旧账号资产。
func TestSlotKeyChangeInvalidatesCache(t *testing.T) {
	var calls int32
	r := newTestResolver(func(_ context.Context, cfg Config) ([]model.Instance, error) {
		atomic.AddInt32(&calls, 1)
		return []model.Instance{{InstanceID: "i-" + cfg.AccessKeyID}}, nil
	})
	old := testCred("s1", "ak-old")
	r.store(old, []model.Instance{{InstanceID: "i-ak-old"}}, model.AccountOverview{})

	fresh := Credential{ID: old.ID, Label: old.Label, AccessKeyID: "ak-new", AccessKeySecret: "sk-new"}
	got, err := r.instances(context.Background(), fresh, false)
	if err != nil || got[0].InstanceID != "i-ak-new" || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("换 AK 后必须重拉: got %v calls=%d err=%v", got, calls, err)
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
	cred := testCred("s1", "ak")

	got, err := r.instances(context.Background(), cred, false)
	if err != nil || got[0].LeaseStartTime != "2026-06-23T06:09:41Z" {
		t.Fatalf("BSS 时间应写入 LeaseStartTime, got %v err=%v", got, err)
	}
	if got[0].CreationTime != "2026-01-01T00:00Z" {
		t.Fatalf("ECS CreationTime 不应被 BSS 覆盖, got %q", got[0].CreationTime)
	}

	// 新鲜期普通读：纯内存命中，BSS 不再被调
	if _, err := r.instances(context.Background(), cred, false); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&bssCalls) != 1 {
		t.Fatalf("新鲜期不应重复调 BSS, calls=%d", bssCalls)
	}

	// 强制刷新：BSS 与 ECS 同批重拉
	if _, err := r.instances(context.Background(), cred, true); err != nil {
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

	got, err := r.instances(context.Background(), testCred("s1", "ak"), false)
	if err != nil || got[0].CreationTime != "2026-01-01T00:00Z" || got[0].LeaseStartTime != "" {
		t.Fatalf("BSS 失败应留空 leaseStart 且保留 ECS 时间, got %v err=%v", got, err)
	}
}

// 账户概览随快照缓存：冷启动写入、Billings 读出、软失败分级缺省。
func TestBillingRidesSnapshot(t *testing.T) {
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		return []model.Instance{{InstanceID: "i-x"}}, nil
	})
	r.fetchBilling = func(context.Context, Config) (model.AccountOverview, error) {
		return model.AccountOverview{
			Available: 7.36, Coupon: 1.5, Currency: "CNY",
			BillingCycle: "2026-09", BillTotal: 108.13,
			BillItems: []model.BillItem{{Product: "云服务器", Amount: 108.13}},
		}, nil
	}
	cred := testCred("s1", "ak")
	creds := []Credential{cred}

	if _, err := r.instances(context.Background(), cred, false); err != nil {
		t.Fatal(err)
	}
	rows := r.Billings(creds)
	if len(rows) != 1 || rows[0].AK != cred.ID || rows[0].Available != 7.36 || rows[0].Coupon != 1.5 || rows[0].BillTotal != 108.13 || len(rows[0].BillItems) != 1 {
		t.Fatalf("概览应随快照可读: %+v", rows)
	}

	// 部分失败：余额成功、账单失败 → 保留余额部分
	r.fetchBilling = func(context.Context, Config) (model.AccountOverview, error) {
		return model.AccountOverview{Available: 7.36, Currency: "CNY"}, errors.New("账单失败")
	}
	if _, err := r.instances(context.Background(), cred, true); err != nil {
		t.Fatal(err)
	}
	if rows := r.Billings(creds); len(rows) != 1 || rows[0].Available != 7.36 || rows[0].BillTotal != 0 {
		t.Fatalf("部分失败应保留余额: %+v", rows)
	}

	// 整体软失败：零概览 → 该 AK 缺省
	r.fetchBilling = func(context.Context, Config) (model.AccountOverview, error) {
		return model.AccountOverview{}, errors.New("bss 全挂")
	}
	if _, err := r.instances(context.Background(), cred, true); err != nil {
		t.Fatal(err)
	}
	if rows := r.Billings(creds); len(rows) != 0 {
		t.Fatalf("零概览应缺省: %+v", rows)
	}
}

// 跨 AK 唯一命中：结果带来源 AK 标识。
func TestEnrichSingleHitCarriesAK(t *testing.T) {
	r := newTestResolver(func(_ context.Context, cfg Config) ([]model.Instance, error) {
		if cfg.AccessKeyID == "ak-a" {
			return []model.Instance{{InstanceID: "i-a"}}, nil
		}
		return []model.Instance{{InstanceID: "i-b"}}, nil
	})
	creds := []Credential{testCred("slot-a", "ak-a"), testCred("slot-b", "ak-b")}

	rows, err := r.Enrich(context.Background(), creds, []Identity{{Instance: "i-b"}}, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("enrich 失败: %v", err)
	}
	if !rows[0].Matched || rows[0].InstanceID != "i-b" || rows[0].AK != "slot-b" || rows[0].AKLabel != "标签-slot-b" {
		t.Fatalf("命中应带来源 AK: %+v", rows[0])
	}
}

// 跨 AK 歧义：两个 AK 各命中一台 → matched=false + note 点名（绝不猜一个）。
func TestEnrichCrossAKAmbiguity(t *testing.T) {
	r := newTestResolver(func(_ context.Context, _ Config) ([]model.Instance, error) {
		return []model.Instance{{InstanceID: "i-1", InstanceName: "web"}}, nil
	})
	creds := []Credential{testCred("slot-a", "ak-a"), testCred("slot-b", "ak-b")}

	rows, err := r.Enrich(context.Background(), creds, []Identity{{Instance: "web"}}, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("enrich 失败: %v", err)
	}
	if rows[0].Matched {
		t.Fatalf("跨 AK 歧义不得 matched: %+v", rows[0])
	}
	if !strings.Contains(rows[0].Note, "跨 2 个 AK") || !strings.Contains(rows[0].Note, "slot-a") || !strings.Contains(rows[0].Note, "slot-b") {
		t.Fatalf("note 应点名全部冲突 AK: %q", rows[0].Note)
	}
}

// 跨 AK 独立降级（2026-10-03 拍板）：单个 AK 拉取失败只跳过该 AK，
// 其它 AK 照常命中；全部失败才整体报错。
func TestSnapshotsIndependentDegradation(t *testing.T) {
	r := newTestResolver(func(_ context.Context, cfg Config) ([]model.Instance, error) {
		if cfg.AccessKeyID == "ak-bad" {
			return nil, errors.New("阿里云抽风")
		}
		return []model.Instance{{InstanceID: "i-ok"}}, nil
	})
	creds := []Credential{testCred("slot-bad", "ak-bad"), testCred("slot-ok", "ak-ok")}

	rows, err := r.Enrich(context.Background(), creds, []Identity{{Instance: "i-ok"}}, false)
	if err != nil || len(rows) != 1 || !rows[0].Matched || rows[0].AK != "slot-ok" {
		t.Fatalf("单 AK 失败不应拖垮其它 AK: rows=%+v err=%v", rows, err)
	}

	// 失败 AK 的资产不可命中：不产生半份结果
	rows2, err := r.Enrich(context.Background(), creds, []Identity{{Instance: "i-ghost"}}, false)
	if err != nil || rows2[0].Matched {
		t.Fatalf("失败 AK 不应有可命中资产: %+v err=%v", rows2, err)
	}

	// 全部失败：错误如实暴露（保持失败可见）
	allBad := []Credential{testCred("s1", "ak-bad"), testCred("s2", "ak-bad")}
	if _, err := r.Enrich(context.Background(), allBad, []Identity{{Instance: "x"}}, false); err == nil {
		t.Fatal("全部 AK 失败应返回错误")
	}
}

// Ensure 全量可用性检查：任何 AK 失败都点名进错误（与 enrich 的独立降级不同）。
func TestEnsureAggregatesAllErrors(t *testing.T) {
	r := newTestResolver(func(_ context.Context, cfg Config) ([]model.Instance, error) {
		if cfg.AccessKeyID != "ak-ok" {
			return nil, fmt.Errorf("地域 %s 失败", cfg.AccessKeyID)
		}
		return []model.Instance{{InstanceID: "i"}}, nil
	})
	creds := []Credential{testCred("s-bad1", "ak-bad1"), testCred("s-ok", "ak-ok"), testCred("s-bad2", "ak-bad2")}
	err := r.Ensure(context.Background(), creds)
	if err == nil || !strings.Contains(err.Error(), "s-bad1") || !strings.Contains(err.Error(), "s-bad2") || strings.Contains(err.Error(), "s-ok") {
		t.Fatalf("Ensure 应点名全部失败 AK 且不受成功者影响: %v", err)
	}
	if err := r.Ensure(context.Background(), []Credential{testCred("s-ok", "ak-ok")}); err != nil {
		t.Fatalf("全部可用不应报错: %v", err)
	}
}

// forEachLimited：有限并发、全部任务必达、并发峰值不超限。
func TestForEachLimited(t *testing.T) {
	var cur, peak, done int32
	var mu sync.Mutex
	forEachLimited(20, 4, func(i int) {
		mu.Lock()
		cur++
		if cur > peak {
			peak = cur
		}
		mu.Unlock()
		time.Sleep(time.Millisecond)
		mu.Lock()
		cur--
		done++
		mu.Unlock()
	})
	if done != 20 {
		t.Fatalf("20 个任务应全部执行, done=%d", done)
	}
	if peak > 4 {
		t.Fatalf("并发峰值应 ≤ 4, got %d", peak)
	}
}
