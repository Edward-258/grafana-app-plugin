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

type Instance struct {
	InstanceID   string   `json:"instanceId"`
	InstanceName string   `json:"instanceName"`
	HostName     string   `json:"hostName"`
	InstanceType string   `json:"instanceType"`
	CPU          int      `json:"cpu"`
	MemoryGiB    int      `json:"memoryGiB"`
	PrivateIPs   []string `json:"privateIps"`
	ZoneID       string   `json:"zoneId"`
	RegionID     string   `json:"regionId"`
}

type ecsClient struct {
	cfg    Config
	http   *http.Client
	host   string
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
		batch, total, err := c.describe(ctx, page, 100)
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

func (c *ecsClient) describe(ctx context.Context, page, size int) ([]Instance, int, error) {
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
	InstanceId           string `json:"InstanceId"`
	InstanceName         string `json:"InstanceName"`
	HostName             string `json:"HostName"`
	InstanceType         string `json:"InstanceType"`
	Cpu                  int    `json:"Cpu"`
	Memory               int    `json:"Memory"`
	ZoneId               string `json:"ZoneId"`
	RegionId             string `json:"RegionId"`
	InnerIpAddress       ipBag  `json:"InnerIpAddress"`
	PublicIpAddress      ipBag  `json:"PublicIpAddress"`
	VpcAttributes        vpcBag `json:"VpcAttributes"`
}

type ipBag struct {
	IpAddress []string `json:"IpAddress"`
}

type vpcBag struct {
	PrivateIpAddress ipBag `json:"PrivateIpAddress"`
}

func (r rawInstance) toInstance() Instance {
	ips := unique(append(append([]string{}, r.VpcAttributes.PrivateIpAddress.IpAddress...), r.InnerIpAddress.IpAddress...))
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
		PrivateIPs:   ips,
		ZoneID:       r.ZoneId,
		RegionID:     r.RegionId,
	}
}

func matchInstance(query string, list []Instance) (Instance, string, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Instance{}, "", false
	}
	host := q
	if i := strings.LastIndex(q, ":"); i > 0 {
		host = q[:i]
	}

	try := func(ok bool, inst Instance, by string) (Instance, string, bool) {
		if ok {
			return inst, by, true
		}
		return Instance{}, "", false
	}

	for _, inst := range list {
		if inst.InstanceID == q || inst.InstanceID == host {
			return try(true, inst, "instanceId")
		}
	}
	for _, inst := range list {
		for _, ip := range inst.PrivateIPs {
			if ip == host || ip == q {
				return try(true, inst, "privateIp")
			}
		}
	}
	for _, inst := range list {
		if inst.HostName != "" && (inst.HostName == host || inst.HostName == q) {
			return try(true, inst, "hostName")
		}
	}
	for _, inst := range list {
		if inst.InstanceName != "" && (inst.InstanceName == host || inst.InstanceName == q) {
			return try(true, inst, "instanceName")
		}
	}
	return Instance{}, "", false
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
