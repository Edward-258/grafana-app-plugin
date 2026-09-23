package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/config"
)

// 凭证自动同步：数据源实例设置独立于 app 插件设置存储，而 Secret 只能写不能
// 读——前端拿不到已存的值，无法「改存」到告警数据源。因此由本进程在启动
// （含配置保存触发的实例重建）时，用 service account（plugin.json iam 声明的
// datasources:write）代为搬运：从内存中的 DecryptedSecureJSONData 读出，经
// Grafana 数据源 API 写入 ds 的 secureJsonData（两端加密存储，不经浏览器）。
//
// iam 里为此新增的 datasources:write 是 Grafana 核心 action，不属于
// %PLUGIN_ID%.* 生成器管辖（同 users.permissions:read 的既有先例）。

// AlertingDatasourceType 是捆绑数据源的插件类型（= package.json name + "-ds"，
// 与 src/datasource/plugin.json 的 %PLUGIN_ID_DS% 同源，pkg/ds 复用此常量）。
const AlertingDatasourceType = PluginID + "-ds"

// akHashKey 存进 ds 的 jsonData：AK ID 的哈希前缀，用于幂等与换 AK 重同步。
// 哈希不泄漏 AK ID 本体（承诺值，非敏感值），故可落在 jsonData。
const akHashKey = "alertingDsAKHash"

// saInfo 从插件初始化 ctx 提取 AppURL 与 SA token；任一不可得（未启用
// externalServiceAccounts 等）返回 false，同步静默跳过。
func saInfo(ctx context.Context) (appURL, token string, ok bool) {
	cfg := config.GrafanaConfigFromContext(ctx)
	token, err := cfg.PluginAppClientSecret()
	if err != nil || token == "" {
		return "", "", false
	}
	appURL, err = cfg.AppURL()
	if err != nil || appURL == "" {
		return "", "", false
	}
	return appURL, token, true
}

// StartAlertingDatasourceSync 在后台执行凭证同步（NewApp 里异步启动，不阻塞
// 实例构造）。重试以覆盖「首次启动时 provisioning 尚未建出 ds 实例」的时序。
func (a *App) StartAlertingDatasourceSync(ctx context.Context, settings backend.AppInstanceSettings) {
	appURL, token, ok := saInfo(ctx)
	if !ok {
		log.DefaultLogger.Warn("告警数据源凭证同步跳过：service account 不可用")
		return
	}
	go func() {
		for attempt := 1; attempt <= 3; attempt++ {
			err := a.syncAlertingDatasource(context.Background(), appURL, token, settings)
			switch {
			case err == nil:
				log.DefaultLogger.Info("告警数据源凭证同步完成")
				return
			case attempt < 3:
				log.DefaultLogger.Warn("告警数据源凭证同步失败，稍后重试", "attempt", attempt, "error", err.Error())
				time.Sleep(10 * time.Second)
			default:
				log.DefaultLogger.Error("告警数据源凭证同步放弃", "error", err.Error())
			}
		}
	}()
}

func (a *App) syncAlertingDatasource(ctx context.Context, appURL, token string, settings backend.AppInstanceSettings) error {
	cfg, err := configFrom(backend.PluginContext{AppInstanceSettings: &settings})
	if err != nil {
		return fmt.Errorf("本 app 未配置凭证: %w", err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	appURL = strings.TrimRight(appURL, "/") // AppURL 带尾斜杠，直接拼接会产生 //api/... 404（同 auth.go JWksURL 先例）
	callJSON := func(method, url string, body, out any) error {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, appURL+url, &buf)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s %s: HTTP %d", method, url, resp.StatusCode)
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	// 数据源实例 uid 不做硬编码（provisioning 之外的安装可能不同）：按类型从
	// 数据源列表里找捆绑 ds。
	var list []struct {
		UID  string `json:"uid"`
		Type string `json:"type"`
	}
	if err := callJSON(http.MethodGet, "/api/datasources", nil, &list); err != nil {
		return fmt.Errorf("列出数据源失败: %w", err)
	}
	uid := ""
	for _, d := range list {
		if d.Type == AlertingDatasourceType {
			uid = d.UID
			break
		}
	}
	if uid == "" {
		return fmt.Errorf("未找到类型为 %s 的数据源实例（provisioning 未生效或被删除）", AlertingDatasourceType)
	}

	// 读全量 → 改两键 → 原样写回。更新接口忽略响应里的多余字段（id/version
	// 等），因此整体回传，无需逐字段复制。
	var cur map[string]any
	if err := callJSON(http.MethodGet, "/api/datasources/uid/"+uid, nil, &cur); err != nil {
		return fmt.Errorf("读取数据源 %s 失败: %w", uid, err)
	}
	secure, _ := cur["secureJsonFields"].(map[string]any)
	jsonData, _ := cur["jsonData"].(map[string]any)
	// 幂等：ds 侧凭证已配置且 AK 哈希一致时跳过（每次启动都写会无谓地触发
	// ds 实例重建）；换 AK（哈希变化）则重同步。
	sum := sha256.Sum256([]byte(cfg.AccessKeyID))
	akHash := hex.EncodeToString(sum[:])[:16]
	if secure["accessKeyId"] == true && secure["accessKeySecret"] == true && jsonData[akHashKey] == akHash {
		return nil
	}
	if jsonData == nil {
		jsonData = map[string]any{}
	}
	jsonData[akHashKey] = akHash
	cur["jsonData"] = jsonData
	cur["secureJsonData"] = map[string]string{
		"accessKeyId":     cfg.AccessKeyID,
		"accessKeySecret": cfg.AccessKeySecret,
	}
	if err := callJSON(http.MethodPut, "/api/datasources/uid/"+uid, cur, nil); err != nil {
		return fmt.Errorf("更新数据源 %s 失败: %w", uid, err)
	}
	return nil
}
