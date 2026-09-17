package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"local-ecs-app/pkg/aliyun/model"
	"local-ecs-app/pkg/aliyun/rpc"
)

type (
	Config      = model.Config
	Instance    = model.Instance
	PublicAsset = model.PublicAsset
	Identity    = model.Identity
)

var ErrNoSettings = model.ErrNoSettings

func MatchIdentity(ident Identity, list []Instance) (Instance, string, string, bool) {
	return model.MatchIdentity(ident, list)
}

func MonitorName(ident Identity) string { return model.MonitorName(ident) }

func RegionSet(list []Instance) []string { return model.RegionSet(list) }

type Client struct {
	cfg  model.Config
	http *http.Client
}

func New(cfg model.Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) endpoint(region string) string {
	return "https://ecs." + region + ".aliyuncs.com/"
}

// List paginates DescribeInstances within one region.
func (c *Client) List(ctx context.Context, region string) ([]model.Instance, error) {
	var all []model.Instance
	for page := 1; page <= 20; page++ {
		batch, total, err := c.describe(ctx, region, page, 100)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(all) >= total || len(batch) == 0 {
			break
		}
	}
	return all, nil
}

func (c *Client) describe(ctx context.Context, region string, page, size int) ([]model.Instance, int, error) {
	body, err := c.call(ctx, c.endpoint(region), map[string]string{
		"Action":     "DescribeInstances",
		"RegionId":   region,
		"PageNumber": strconv.Itoa(page),
		"PageSize":   strconv.Itoa(size),
	})
	if err != nil {
		return nil, 0, err
	}
	var parsed describeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("解析 ECS 响应失败: %w", err)
	}
	out := make([]model.Instance, 0, len(parsed.Instances.Instance))
	for _, raw := range parsed.Instances.Instance {
		out = append(out, raw.toInstance())
	}
	return out, parsed.TotalCount, nil
}

// call signs and posts one RPC request and returns the raw body of a healthy response.
func (c *Client) call(ctx context.Context, host string, action map[string]string) ([]byte, error) {
	params := map[string]string{
		"AccessKeyId":      c.cfg.AccessKeyID,
		"Format":           "JSON",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   strconv.FormatInt(time.Now().UnixNano(), 10),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2014-05-26",
	}
	for k, v := range action {
		params[k] = v
	}
	params["Signature"] = rpc.Sign("POST", params, c.cfg.AccessKeySecret)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var env apiEnvelope
	_ = json.Unmarshal(body, &env)
	if env.Code != "" && env.Message != "" {
		return nil, fmt.Errorf("阿里云 %s: %s", env.Code, env.Message)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("阿里云 HTTP %d: %s", res.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}
