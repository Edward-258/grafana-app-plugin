package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

func IPFromIdentity(s string) string { return model.IPFromIdentity(s) }

func MatchIdentity(ident Identity, list []Instance) (Instance, string, bool) {
	return model.MatchIdentity(ident, list)
}

func MonitorName(ident Identity) string { return model.MonitorName(ident) }

type Client struct {
	cfg  model.Config
	http *http.Client
	host string
}

func New(cfg model.Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 20 * time.Second},
		host: "https://ecs." + cfg.Region + ".aliyuncs.com/",
	}
}

func (c *Client) List(ctx context.Context) ([]model.Instance, error) {
	var all []model.Instance
	for page := 1; page <= 20; page++ {
		batch, total, err := c.describe(ctx, page, 100, nil)
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

func (c *Client) GetByIP(ctx context.Context, ip string) (model.Instance, bool, error) {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return model.Instance{}, false, nil
	}
	filters := []map[string]string{
		{"PrivateIpAddresses.1": ip},
		{"InnerIpAddresses.1": ip},
		{"PublicIpAddresses.1": ip},
	}
	for _, extra := range filters {
		batch, _, err := c.describe(ctx, 1, 10, extra)
		if err != nil {
			return model.Instance{}, false, err
		}
		if len(batch) > 0 {
			return batch[0], true, nil
		}
	}
	return model.Instance{}, false, nil
}

func (c *Client) describe(ctx context.Context, page, size int, extra map[string]string) ([]model.Instance, int, error) {
	params := map[string]string{
		"Action":           "DescribeInstances",
		"Format":           "JSON",
		"Version":          "2014-05-26",
		"AccessKeyId":      c.cfg.AccessKeyID,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   strconv.FormatInt(time.Now().UnixNano(), 10),
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"RegionId":         c.cfg.Region,
		"PageNumber":       strconv.Itoa(page),
		"PageSize":         strconv.Itoa(size),
	}
	for k, v := range extra {
		params[k] = v
	}
	params["Signature"] = rpc.Sign("POST", params, c.cfg.AccessKeySecret)

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, 0, err
	}

	var parsed describeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("解析 ECS 响应失败: %w", err)
	}
	if err := apiError(parsed, res.StatusCode, body); err != nil {
		return nil, 0, err
	}

	out := make([]model.Instance, 0, len(parsed.Instances.Instance))
	for _, raw := range parsed.Instances.Instance {
		out = append(out, raw.toInstance())
	}
	return out, parsed.TotalCount, nil
}
