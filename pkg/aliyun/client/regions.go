package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"local-ecs-app/pkg/aliyun/model"
)

// DescribeRegions is account-wide, so any regional endpoint answers for all of them.
const regionsHost = "https://ecs.cn-hangzhou.aliyuncs.com/"

type regionsResponse struct {
	Regions struct {
		Region []struct {
			RegionId string `json:"RegionId"`
		} `json:"Region"`
	} `json:"Regions"`
}

// DescribeRegions lists every region the account can use.
func (c *Client) DescribeRegions(ctx context.Context) ([]string, error) {
	body, err := c.call(ctx, c.regionsHost, map[string]string{"Action": "DescribeRegions"})
	if err != nil {
		return nil, err
	}
	var parsed regionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析地域响应失败: %w", err)
	}
	out := make([]string, 0, len(parsed.Regions.Region))
	for _, r := range parsed.Regions.Region {
		if r.RegionId != "" {
			out = append(out, r.RegionId)
		}
	}
	return out, nil
}

// ListAll enumerates instances in every region the AK is authorized to see.
// One AK may own machines spread across regions, and a failed region would
// leave the asset picture silently incomplete, so any region error fails the
// whole call. The one exception is Forbidden.RAM: an AK whose policy is
// scoped to specific instances gets denied in every other region, which is
// the credential's visibility boundary rather than a data-integrity gap.
// Skipped regions don't count toward completeness; if every region is denied,
// the AK sees nothing and we still fail loudly.
func (c *Client) ListAll(ctx context.Context) ([]model.Instance, error) {
	regions, err := c.DescribeRegions(ctx)
	if err != nil {
		return nil, err
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		denied   int
	)
	all := make([]model.Instance, 0)
	sem := make(chan struct{}, 6)
	for _, region := range regions {
		wg.Add(1)
		go func(region string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			list, err := c.List(ctx, region)
			if isThrottling(err) {
				time.Sleep(500 * time.Millisecond)
				list, err = c.List(ctx, region)
			}
			if err != nil {
				mu.Lock()
				switch {
				case IsRamDenied(err):
					denied++
				case firstErr == nil:
					firstErr = fmt.Errorf("地域 %s: %w", region, err)
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			all = append(all, list...)
			mu.Unlock()
		}(region)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, fmt.Errorf("全地域枚举不完整: %w", firstErr)
	}
	if len(regions) > 0 && denied == len(regions) {
		return nil, fmt.Errorf("全部 %d 个地域均被 RAM 拒绝（Forbidden.RAM）：该 AK 的授权范围不覆盖 DescribeInstances（或实例收束策略未命中任何地域），无资产可枚举", len(regions))
	}
	return all, nil
}

func isThrottling(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Throttl")
}
