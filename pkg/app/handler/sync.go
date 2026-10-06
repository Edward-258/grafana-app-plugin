package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/config"

	"local-ecs-app/pkg/app/service"
)

// 凭证自动同步：数据源实例设置独立于 app 插件设置存储，而 Secret 只能写不能
// 读——前端拿不到已存的值，无法「改存」到告警数据源。因此由本进程在启动
// （含配置保存触发的实例重建）时，用 service account（plugin.json iam 声明的
// datasources:write）代为搬运：从内存中的 DecryptedSecureJSONData 读出，经
// Grafana 数据源 API 写入 ds 的 secureJsonData（两端加密存储，不经浏览器）。
//
// 多 AK：app 的全部插槽整体覆写到 ds（键名 ak:<slot>:* 与 app 同 scheme），
// jsonData.akList 携带 slot+label（非敏感值，红线4 允许）；ds 现有的 ak:* 与
// legacy 键先全部空串清掉再写入期望值，删除的插槽、旧格式键随之消失。
//
// 不做幂等跳过：ds 的 secureJsonFields 只暴露「是否已配置」，比不出插槽内换
// AK；要比就得往 jsonData 放凭证指纹，违背红线4 的零敏感物料。代价是每次实例
// 构造（启动/配置保存）多一次 ds 写入、ds 缓存冷一轮。app 设置是唯一真源，
// 删光插槽同样同步（ds 清空，不留已删凭证继续跑告警）。
//
// iam 里为此新增的 datasources:write 是 Grafana 核心 action，不属于
// %PLUGIN_ID%.* 生成器管辖（同 users.permissions:read 的既有先例）。

// AlertingDatasourceType 是捆绑数据源的插件类型（= package.json name + "-ds"，
// 与 src/datasource/plugin.json 的 %PLUGIN_ID_DS% 同源，pkg/ds 复用此常量）。
const AlertingDatasourceType = PluginID + "-ds"

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
	// 只有「一对都没有」继续往下走（清空 ds）；超限等其它错误不碰 ds
	creds, err := credentialsFrom(backend.PluginContext{AppInstanceSettings: &settings})
	if err != nil && !errors.Is(err, service.ErrNoSettings) {
		return fmt.Errorf("读取本 app 凭证失败: %w", err)
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

	// 读全量 → 改 jsonData.akList 与 secureJsonData → 原样写回。更新接口忽略
	// 响应里的多余字段（id/version 等），因此整体回传，无需逐字段复制。
	var cur map[string]any
	if err := callJSON(http.MethodGet, "/api/datasources/uid/"+uid, nil, &cur); err != nil {
		return fmt.Errorf("读取数据源 %s 失败: %w", uid, err)
	}
	secure, _ := cur["secureJsonFields"].(map[string]any)
	jsonData, _ := cur["jsonData"].(map[string]any)
	if jsonData == nil {
		jsonData = map[string]any{}
	}

	sec := map[string]string{}
	for k := range secure {
		if strings.HasPrefix(k, "ak:") || k == "accessKeyId" || k == "accessKeySecret" {
			sec[k] = ""
		}
	}
	want := make([]AKSlot, 0, len(creds))
	for _, c := range creds {
		want = append(want, AKSlot{Slot: c.ID, Label: c.Label})
		sec[SecureKeyID(c.ID)] = c.AccessKeyID
		sec[SecureKeySecret(c.ID)] = c.AccessKeySecret
	}
	jsonData["akList"] = want
	delete(jsonData, "alertingDsAKHash") // 旧单对幂等标记退役
	cur["jsonData"] = jsonData
	cur["secureJsonData"] = sec
	if err := callJSON(http.MethodPut, "/api/datasources/uid/"+uid, cur, nil); err != nil {
		return fmt.Errorf("更新数据源 %s 失败: %w", uid, err)
	}
	return nil
}
