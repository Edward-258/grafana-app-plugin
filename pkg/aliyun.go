package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errNoSettings = errors.New("未配置 AccessKey，请到插件配置页填写 Region / AccessKey")

type Config struct {
	Region          string
	AccessKeyID     string
	AccessKeySecret string
}

// Instance is backend-only. IP fields never go to the browser.
type Instance struct {
	InstanceID   string
	InstanceName string
	HostName     string
	InstanceType string
	CPU          int
	MemoryGiB    int
	PrivateIPs   []string
	PublicIPs    []string
	ZoneID       string
	RegionID     string
}

// PublicAsset is the payload returned to the plugin UI. No addresses.
type PublicAsset struct {
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	HostName     string `json:"hostName"`
	InstanceType string `json:"instanceType"`
	CPU          int    `json:"cpu"`
	MemoryGiB    int    `json:"memoryGiB"`
	ZoneID       string `json:"zoneId,omitempty"`
	RegionID     string `json:"regionId,omitempty"`
}

func (i Instance) Public() PublicAsset {
	return PublicAsset{
		InstanceID:   i.InstanceID,
		InstanceName: i.InstanceName,
		HostName:     i.HostName,
		InstanceType: i.InstanceType,
		CPU:          i.CPU,
		MemoryGiB:    i.MemoryGiB,
		ZoneID:       i.ZoneID,
		RegionID:     i.RegionID,
	}
}

type ecsClient struct {
	cfg  Config
	http *http.Client
	host string
}

func newECSClient(cfg Config) *ecsClient {
	return &ecsClient{
		cfg:  cfg,
		http: &http.Client{Timeout: 20 * time.Second},
		host: "https://ecs." + cfg.Region + ".aliyuncs.com/",
	}
}

func (c *ecsClient) List(ctx context.Context) ([]Instance, error) {
	var all []Instance
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

func (c *ecsClient) GetByIP(ctx context.Context, ip string) (Instance, bool, error) {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return Instance{}, false, nil
	}
	filters := []map[string]string{
		{"PrivateIpAddresses.1": ip},
		{"InnerIpAddresses.1": ip},
		{"PublicIpAddresses.1": ip},
	}
	for _, extra := range filters {
		batch, _, err := c.describe(ctx, 1, 10, extra)
		if err != nil {
			return Instance{}, false, err
		}
		if len(batch) > 0 {
			return batch[0], true, nil
		}
	}
	return Instance{}, false, nil
}

func (c *ecsClient) describe(ctx context.Context, page, size int, extra map[string]string) ([]Instance, int, error) {
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
	params["Signature"] = signRPC("POST", params, c.cfg.AccessKeySecret)

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
	if parsed.Code != "" && parsed.Message != "" {
		return nil, 0, fmt.Errorf("阿里云 %s: %s", parsed.Code, parsed.Message)
	}
	if res.StatusCode >= 400 {
		return nil, 0, fmt.Errorf("阿里云 HTTP %d: %s", res.StatusCode, truncate(string(body), 300))
	}

	out := make([]Instance, 0, len(parsed.Instances.Instance))
	for _, raw := range parsed.Instances.Instance {
		out = append(out, raw.toInstance())
	}
	return out, parsed.TotalCount, nil
}

type describeResponse struct {
	Code       string `json:"Code"`
	Message    string `json:"Message"`
	TotalCount int    `json:"TotalCount"`
	Instances  struct {
		Instance []rawInstance `json:"Instance"`
	} `json:"Instances"`
}

type rawInstance struct {
	InstanceId      string `json:"InstanceId"`
	InstanceName    string `json:"InstanceName"`
	HostName        string `json:"HostName"`
	InstanceType    string `json:"InstanceType"`
	Cpu             int    `json:"Cpu"`
	Memory          int    `json:"Memory"`
	ZoneId          string `json:"ZoneId"`
	RegionId        string `json:"RegionId"`
	InnerIpAddress  ipBag  `json:"InnerIpAddress"`
	PublicIpAddress ipBag  `json:"PublicIpAddress"`
	VpcAttributes   vpcBag `json:"VpcAttributes"`
	EipAddress      struct {
		IpAddress string `json:"IpAddress"`
	} `json:"EipAddress"`
}

type ipBag struct {
	IpAddress []string `json:"IpAddress"`
}

type vpcBag struct {
	PrivateIpAddress ipBag `json:"PrivateIpAddress"`
}

func (r rawInstance) toInstance() Instance {
	priv := unique(append(append([]string{}, r.VpcAttributes.PrivateIpAddress.IpAddress...), r.InnerIpAddress.IpAddress...))
	pub := unique(append(append([]string{}, r.PublicIpAddress.IpAddress...), r.EipAddress.IpAddress))
	mem := r.Memory / 1024
	if mem == 0 && r.Memory > 0 {
		mem = 1
	}
	return Instance{
		InstanceID:   r.InstanceId,
		InstanceName: r.InstanceName,
		HostName:     r.HostName,
		InstanceType: r.InstanceType,
		CPU:          r.Cpu,
		MemoryGiB:    mem,
		PrivateIPs:   priv,
		PublicIPs:    pub,
		ZoneID:       r.ZoneId,
		RegionID:     r.RegionId,
	}
}

func identityHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i]
		}
	}
	return s
}

func ipFromIdentity(s string) string {
	host := identityHost(s)
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}

func matchInstance(query string, list []Instance) (Instance, string, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Instance{}, "", false
	}
	host := identityHost(q)

	for _, inst := range list {
		if inst.InstanceID == q || inst.InstanceID == host {
			return inst, "instanceId", true
		}
	}
	for _, inst := range list {
		for _, ip := range inst.PrivateIPs {
			if ip == host || ip == q {
				return inst, "privateIp", true
			}
		}
		for _, ip := range inst.PublicIPs {
			if ip == host || ip == q {
				return inst, "publicIp", true
			}
		}
	}
	for _, inst := range list {
		if inst.HostName != "" && (inst.HostName == host || inst.HostName == q) {
			return inst, "hostName", true
		}
	}
	for _, inst := range list {
		if inst.InstanceName != "" && (inst.InstanceName == host || inst.InstanceName == q) {
			return inst, "instanceName", true
		}
	}
	return Instance{}, "", false
}

func matchIdentity(ident promIdentity, list []Instance) (Instance, string, bool) {
	for _, q := range []string{ident.Instance, ident.NodeName, ident.IP} {
		if inst, by, ok := matchInstance(q, list); ok {
			return inst, by, true
		}
	}
	return Instance{}, "", false
}

func monitorName(ident promIdentity) string {
	if ident.NodeName != "" && ipFromIdentity(ident.NodeName) == "" {
		return ident.NodeName
	}
	host := identityHost(ident.Instance)
	if host != "" && ipFromIdentity(host) == "" {
		return host
	}
	return ""
}

func signRPC(method string, params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, percentEncode(k)+"="+percentEncode(params[k]))
	}
	canonical := strings.Join(pairs, "&")
	stringToSign := method + "&" + percentEncode("/") + "&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func percentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func unique(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
