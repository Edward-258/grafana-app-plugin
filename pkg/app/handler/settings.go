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
	if pCtx.AppInstanceSettings == nil {
		return cfg, service.ErrNoSettings
	}
	var data jsonData
	if len(pCtx.AppInstanceSettings.JSONData) > 0 {
		_ = json.Unmarshal(pCtx.AppInstanceSettings.JSONData, &data)
		cfg.AccessKeyID = data.AccessKeyID
	}
	if pCtx.AppInstanceSettings.DecryptedSecureJSONData != nil {
		cfg.AccessKeySecret = pCtx.AppInstanceSettings.DecryptedSecureJSONData["accessKeySecret"]
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return cfg, service.ErrNoSettings
	}
	return cfg, nil
}
