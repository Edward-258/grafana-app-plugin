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
	body, err := c.call(ctx, regionsHost, map[string]string{"Action": "DescribeRegions"})
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

// ListAll enumerates instances in every region the AK can see. One AK may own
// machines spread across regions, and a failed region would leave the asset
// picture silently incomplete, so any region error fails the whole call.
func (c *Client) ListAll(ctx context.Context) ([]model.Instance, error) {
	regions, err := c.DescribeRegions(ctx)
	if err != nil {
		return nil, err
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
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
				if firstErr == nil {
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
	return all, nil
}

func isThrottling(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Throttl")
}
