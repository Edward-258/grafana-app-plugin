package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

type App struct {
	backend.CallResourceHandler

	mu    sync.Mutex
	cache *ecsCache
}

type ecsCache struct {
	key       string
	at        time.Time
	instances []Instance
}

func NewApp(_ context.Context, _ backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	a := &App{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/ecs/instances", a.handleList)
	mux.HandleFunc("/ecs/resolve", a.handleResolve)
	mux.HandleFunc("/ecs/test", a.handleTest)
	a.CallResourceHandler = httpadapter.New(mux)
	return a, nil
}

func (a *App) Dispose() {}

func (a *App) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	cfg, err := configFrom(req.PluginContext)
	if err != nil || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusUnknown,
			Message: "未配置 AccessKey",
		}, nil
	}
	if _, err := a.instances(ctx, cfg); err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "ok"}, nil
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	list, err := a.instances(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": list})
}

func (a *App) handleResolve(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	q := r.URL.Query().Get("q")
	list, err := a.instances(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	inst, by, ok := matchInstance(q, list)
	resp := map[string]any{"query": q, "matched": ok}
	if ok {
		resp["instance"] = inst
		resp["matchedBy"] = by
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, err := newECSClient(cfg).List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.storeCache(cfg, list)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(list)})
}

func (a *App) instances(ctx context.Context, cfg Config) ([]Instance, error) {
	key := cfg.Region + "|" + cfg.AccessKeyID
	a.mu.Lock()
	if a.cache != nil && a.cache.key == key && time.Since(a.cache.at) < 5*time.Minute {
		out := a.cache.instances
		a.mu.Unlock()
		return out, nil
	}
	a.mu.Unlock()

	list, err := newECSClient(cfg).List(ctx)
	if err != nil {
		return nil, err
	}
	a.storeCache(cfg, list)
	return list, nil
}

func (a *App) storeCache(cfg Config, list []Instance) {
	a.mu.Lock()
	a.cache = &ecsCache{key: cfg.Region + "|" + cfg.AccessKeyID, at: time.Now(), instances: list}
	a.mu.Unlock()
}

type jsonData struct {
	Region      string `json:"region"`
	AccessKeyID string `json:"accessKeyId"`
}

func configFromRequest(r *http.Request) (Config, error) {
	return configFrom(backend.PluginConfigFromContext(r.Context()))
}

func configFrom(pCtx backend.PluginContext) (Config, error) {
	cfg := Config{Region: "cn-hangzhou"}
	if pCtx.AppInstanceSettings == nil {
		return cfg, errNoSettings
	}
	var data jsonData
	if len(pCtx.AppInstanceSettings.JSONData) > 0 {
		_ = json.Unmarshal(pCtx.AppInstanceSettings.JSONData, &data)
		if data.Region != "" {
			cfg.Region = data.Region
		}
		cfg.AccessKeyID = data.AccessKeyID
	}
	if pCtx.AppInstanceSettings.DecryptedSecureJSONData != nil {
		cfg.AccessKeySecret = pCtx.AppInstanceSettings.DecryptedSecureJSONData["accessKeySecret"]
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return cfg, errNoSettings
	}
	return cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
