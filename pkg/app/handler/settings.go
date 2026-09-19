package handler

import (
	"encoding/json"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"local-ecs-app/pkg/app/service"
)

type jsonData struct {
	AccessKeyID string `json:"accessKeyId"`
}

func configFromRequest(r *http.Request) (service.Config, error) {
	return configFrom(backend.PluginConfigFromContext(r.Context()))
}

func configFrom(pCtx backend.PluginContext) (service.Config, error) {
	cfg := service.Config{}
	if pCtx.AppInstanceSettings != nil {
		if id, _, ok := currentAccessKeyID(pCtx); ok {
			cfg.AccessKeyID = id
		}
		cfg.AccessKeySecret = pCtx.AppInstanceSettings.DecryptedSecureJSONData["accessKeySecret"]
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return cfg, service.ErrNoSettings
	}
	return cfg, nil
}

// currentAccessKeyID 返回当前生效的 AccessKey ID：secureJsonData 副本优先，
// 旧格式（jsonData）兜底。legacy 表示只存在旧副本，保存一次即可完成迁移。
func currentAccessKeyID(pCtx backend.PluginContext) (value string, legacy bool, configured bool) {
	if pCtx.AppInstanceSettings == nil {
		return "", false, false
	}
	if v := pCtx.AppInstanceSettings.DecryptedSecureJSONData["accessKeyId"]; v != "" {
		return v, false, true
	}
	var data jsonData
	if len(pCtx.AppInstanceSettings.JSONData) > 0 {
		_ = json.Unmarshal(pCtx.AppInstanceSettings.JSONData, &data)
	}
	if data.AccessKeyID != "" {
		return data.AccessKeyID, true, true
	}
	return "", false, false
}
